package hostname

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	netv1 "k8s.io/api/networking/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"

	"cosmossdk.io/log"

	manifest "pkg.akt.dev/go/manifest/v2beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube"
	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	"github.com/akash-network/provider/cluster/kube/gateway"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	clusterutil "github.com/akash-network/provider/cluster/util"
	"github.com/akash-network/provider/operator/common"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/fromctx"
)

var (
	errExpectedResourceNotFound = fmt.Errorf("%w: resource not found", common.ErrObservationStopped)
)

type hostnameOperator struct {
	ctx               context.Context
	hostnames         map[string]managedHostname
	pending           map[hostnameWorkKey]pendingHostname
	ns                string
	log               log.Logger
	kc                kubernetes.Interface
	ac                akashclientset.Interface
	dc                dynamic.Interface
	cfg               common.OperatorConfig
	server            common.OperatorHTTP
	flagHostnamesData common.PrepareFlagFn
	flagPendingData   common.PrepareFlagFn
	ingressConfig     kube.IngressConfig
	gatewayImpl       gateway.GatewayProvider
}

func newHostnameOperator(ctx context.Context, logger log.Logger, ns string, config common.OperatorConfig) (*hostnameOperator, error) {
	kc, err := fromctx.KubeClientFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	ac, err := fromctx.AkashClientFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	kubecfg, err := fromctx.KubeConfigFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	dc, err := dynamic.NewForConfig(kubecfg)
	if err != nil {
		return nil, fmt.Errorf("hostname operator: unable to create dynamic client: %w", err)
	}

	opHTTP, err := common.NewOperatorHTTP()
	if err != nil {
		return nil, err
	}

	gwCfg := fromctx.MustGatewayConfigFromCtx(ctx)
	ingressMode := builder.IngressMode(gwCfg.IngressMode)

	// Initialize Gateway provider if using gateway-api mode
	var gatewayImpl gateway.GatewayProvider
	if ingressMode == builder.IngressModeGateway {
		impl, err := gateway.GetProvider(gwCfg.Provider, logger)
		if err != nil {
			return nil, fmt.Errorf("hostname operator: failed to get gateway provider: %w", err)
		}
		gatewayImpl = impl
		logger.Info("initialized gateway provider",
			"provider", impl.Name())
	}

	logger.Info("initializing hostname operator",
		"ingress-mode", gwCfg.IngressMode,
		"gateway-name", gwCfg.Name,
		"gateway-namespace", gwCfg.Namespace,
		"gateway-provider", gwCfg.Provider)

	op := &hostnameOperator{
		ctx:       ctx,
		hostnames: make(map[string]managedHostname),
		ns:        ns,
		log:       logger,
		kc:        kc,
		ac:        ac,
		dc:        dc,
		cfg:       config,
		server:    opHTTP,
		pending:   make(map[hostnameWorkKey]pendingHostname),
		ingressConfig: kube.IngressConfig{
			IngressMode:      ingressMode,
			GatewayName:      gwCfg.Name,
			GatewayNamespace: gwCfg.Namespace,
		},
		gatewayImpl: gatewayImpl,
	}

	op.flagPendingData = op.server.AddPreparedEndpoint("/pending-hostnames", op.preparePendingData)
	op.flagHostnamesData = op.server.AddPreparedEndpoint("/managed-hostnames", op.prepareHostnamesData)

	return op, nil
}

func (op *hostnameOperator) run() error {
	op.log.Debug("hostname operator start")

	for {
		lastAttempt := time.Now()
		err := op.monitorUntilError()
		if errors.Is(err, context.Canceled) {
			op.log.Debug("hostname operator terminate")
			return err
		}

		op.log.Error("observation stopped", "err", err)

		// don't spin if there is a condition causing fast failure
		elapsed := time.Since(lastAttempt)
		if elapsed < op.cfg.RetryDelay {
			op.log.Info("delaying")
			select {
			case <-op.ctx.Done():
				return op.ctx.Err()
			case <-time.After(op.cfg.RetryDelay):
				// delay complete
			}
		}
	}
}

func (op *hostnameOperator) monitorUntilError() error {
	op.hostnames = make(map[string]managedHostname)
	ctx, cancel := context.WithCancel(op.ctx)
	defer cancel()

	op.log.Info("starting observation")

	connections, err := op.getHostnameDeploymentConnections(ctx)
	if err != nil {
		op.log.Error("unable to get connections", "err", err.Error())
		return err
	}

	for _, conn := range connections {
		leaseID := conn.GetLeaseID()
		hostname := conn.GetHostname()
		entry := managedHostname{
			lastEvent:           nil,
			presentLease:        leaseID,
			presentServiceName:  conn.GetServiceName(),
			presentExternalPort: uint32(conn.GetExternalPort()), // nolint: gosec
		}

		op.hostnames[hostname] = entry
		// Recheck actual connections against current desired state, including
		// deletions missed while the watch was disconnected.
		op.queueHostname(hostnameResourceEvent{
			eventType: ctypes.ProviderResourceUpdate, hostname: hostname, leaseID: leaseID,
			serviceName: conn.GetServiceName(), externalPort: uint32(conn.GetExternalPort()), // nolint: gosec
		})
		op.log.Debug("identified existing hostname connection",
			"hostname", hostname,
			"lease", entry.presentLease,
			"service", entry.presentServiceName,
			"port", entry.presentExternalPort)
	}
	op.flagHostnamesData()

	events, err := op.observeHostnameState(ctx)
	if err != nil {
		return err
	}

	resyncTicker := time.NewTicker(op.cfg.PruneInterval)
	defer resyncTicker.Stop()
	prepareTicker := time.NewTicker(op.cfg.WebRefreshInterval)
	defer prepareTicker.Stop()
	retryTimer := time.NewTimer(time.Hour)
	defer retryTimer.Stop()

	for {
		var retryCh <-chan time.Time
		if delay, pending := op.nextHostnameRetry(); pending {
			retryTimer.Reset(delay)
			retryCh = retryTimer.C
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return common.ErrObservationStopped
			}
			op.queueHostname(ev)
		case <-retryCh:
			op.retryHostname(ctx)
		case <-resyncTicker.C:
			// Queue the complete snapshot without restarting the watch. Restarting
			// during replay can repeatedly discard its tail on a busy provider.
			if err := op.refreshHostnames(ctx); err != nil {
				op.log.Error("unable to refresh hostnames; will retry", "err", err)
			}
		case <-prepareTicker.C:
			if err := op.server.PrepareAll(); err != nil {
				op.log.Error("preparing web data failed", "err", err)
			}
		}
	}
}

func (op *hostnameOperator) prepareHostnamesData(pd common.PreparedResult) error {
	op.log.Debug("preparing managed-hostnames")
	buf := &bytes.Buffer{}
	data := make(map[string]interface{})

	for hostname, entry := range op.hostnames {
		preparedEntry := struct {
			LeaseID      mtypes.LeaseID
			Namespace    string
			ExternalPort uint32
			ServiceName  string
			LastUpdate   string
		}{
			LeaseID:      entry.presentLease,
			Namespace:    clusterutil.LeaseIDToNamespace(entry.presentLease),
			ExternalPort: entry.presentExternalPort,
			ServiceName:  entry.presentServiceName,
			LastUpdate:   entry.lastChangeAt.String(),
		}
		data[hostname] = preparedEntry
	}

	enc := json.NewEncoder(buf)
	err := enc.Encode(data)
	if err != nil {
		return err
	}

	pd.Set(buf.Bytes())
	return nil
}

func (op *hostnameOperator) applyDeleteEvent(ctx context.Context, ev chostname.ResourceEvent) error {
	leaseID := ev.GetLeaseID()
	err := op.removeHostnameFromDeployment(ctx, ev.GetHostname(), leaseID, true)

	if err == nil {
		if entry, exists := op.hostnames[ev.GetHostname()]; exists && entry.presentLease.Equals(leaseID) {
			delete(op.hostnames, ev.GetHostname())
			op.flagHostnamesData()
		}
	}

	return err
}

func buildDirective(ev chostname.ResourceEvent, serviceExpose crd.ManifestServiceExpose) chostname.ConnectToDeploymentDirective {
	// Build the directive based off the event
	directive := chostname.ConnectToDeploymentDirective{
		Hostname:    ev.GetHostname(),
		LeaseID:     ev.GetLeaseID(),
		ServiceName: ev.GetServiceName(),
		ServicePort: int32(ev.GetExternalPort()), // nolint: gosec
	}
	/*
		Populate the configuration options
		selectedExpose.HttpOptions has zero values if this is from an earlier CRD. Just insert
		defaults and move on
	*/
	if serviceExpose.HTTPOptions.MaxBodySize == 0 {
		directive.ReadTimeout = 60000
		directive.SendTimeout = 60000
		directive.NextTimeout = 60000
		directive.MaxBodySize = 1048576
		directive.NextTries = 3
		directive.NextCases = []string{"error", "timeout"}
	} else {
		directive.ReadTimeout = serviceExpose.HTTPOptions.ReadTimeout
		directive.SendTimeout = serviceExpose.HTTPOptions.SendTimeout
		directive.NextTimeout = serviceExpose.HTTPOptions.NextTimeout
		directive.MaxBodySize = serviceExpose.HTTPOptions.MaxBodySize
		directive.NextTries = serviceExpose.HTTPOptions.NextTries
		directive.NextCases = serviceExpose.HTTPOptions.NextCases
	}

	return directive
}

func (op *hostnameOperator) locateServiceFromManifest(ctx context.Context, leaseID mtypes.LeaseID, serviceName string, externalPort uint32) (crd.ManifestServiceExpose, error) {
	// Locate the matching service name & expose directive in the manifest CRD
	found, manifestGroup, err := op.getManifestGroup(ctx, leaseID)
	if err != nil {
		return crd.ManifestServiceExpose{}, err
	}
	if !found {
		/*
			It's possible this code could race to read the CRD, although unlikely. If this fails the operator
			restarts and should work the attempt anyways. If this becomes a pain point then the operator
			can be rewritten to watch for CRD events on the manifest as well, then avoid running this code
			until the manifest exists.
		*/
		return crd.ManifestServiceExpose{}, fmt.Errorf("%w: manifest for %v", errExpectedResourceNotFound, leaseID)
	}

	var selectedService crd.ManifestService
	for _, service := range manifestGroup.Services {
		if service.Name == serviceName {
			selectedService = service
			break
		}
	}

	if selectedService.Count == 0 {
		return crd.ManifestServiceExpose{}, fmt.Errorf("%w: service for %v - %v", errExpectedResourceNotFound, leaseID, serviceName)
	}

	var selectedExpose crd.ManifestServiceExpose
	for _, expose := range selectedService.Expose {
		if !expose.Global {
			continue
		}

		mse := manifest.ServiceExpose{
			Port:         uint32(expose.Port),
			ExternalPort: uint32(expose.ExternalPort),
		}

		if externalPort == uint32(mse.GetExternalPort()) { // nolint: gosec
			selectedExpose = expose
			break
		}
	}

	if selectedExpose.Port == 0 {
		return crd.ManifestServiceExpose{}, fmt.Errorf("%w: service expose for %v - %v - %v", errExpectedResourceNotFound, leaseID, serviceName, externalPort)
	}

	return selectedExpose, nil
}

func (op *hostnameOperator) applyAddOrUpdateEvent(ctx context.Context, ev chostname.ResourceEvent) error {
	selectedExpose, err := op.locateServiceFromManifest(ctx, ev.GetLeaseID(), ev.GetServiceName(), ev.GetExternalPort())
	if err != nil {
		return err
	}

	leaseID := ev.GetLeaseID()

	op.log.Debug("connecting",
		"hostname", ev.GetHostname(),
		"lease", leaseID,
		"service", ev.GetServiceName(),
		"externalPort", ev.GetExternalPort())
	entry, exists := op.hostnames[ev.GetHostname()]

	isSameLease := false
	if exists {
		isSameLease = entry.presentLease.Equals(leaseID)
	} else {
		isSameLease = true
	}

	directive := buildDirective(ev, selectedExpose)

	if isSameLease {
		// shouldConnect := false

		if !exists {
			// shouldConnect = true
			op.log.Debug("hostname target is new, applying")
			// Check to see if port or service name is different
		} else if entry.presentExternalPort != ev.GetExternalPort() || entry.presentServiceName != ev.GetServiceName() {
			// shouldConnect = true
			op.log.Debug("hostname target has changed, applying")
		}

		// if shouldConnect {
		op.log.Debug("Updating ingress")
		// Update or create the existing ingress
		err = op.connectHostnameToDeployment(ctx, directive)
		// }
	} else {
		op.log.Debug("Swapping ingress to new deployment")
		//  Delete the ingress in one namespace and recreate it in the correct one
		err = op.removeHostnameFromDeployment(ctx, ev.GetHostname(), entry.presentLease, true)
		if err == nil {
			// Remove the current entry, if the next action succeeds then it gets inserted below
			delete(op.hostnames, ev.GetHostname())
			err = op.connectHostnameToDeployment(ctx, directive)
		}
	}

	if err == nil { // Update stored entry if everything went OK
		entry.presentExternalPort = ev.GetExternalPort()
		entry.presentServiceName = ev.GetServiceName()
		entry.presentLease = leaseID
		entry.lastEvent = ev
		entry.lastChangeAt = time.Now()
		op.hostnames[ev.GetHostname()] = entry
		op.flagHostnamesData()
	}

	return err
}

func (op *hostnameOperator) getHostnameDeploymentConnections(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	if op.ingressConfig.IngressMode == builder.IngressModeGateway {
		return op.getHostnameDeploymentConnectionsGateway(ctx)
	}

	ingressPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return op.kc.NetworkingV1().Ingresses(metav1.NamespaceAll).List(ctx, opts)
	})

	results := make([]chostname.LeaseIDConnection, 0)
	err := ingressPager.EachListItem(ctx,
		metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=true", builder.AkashManagedLabelName)},
		func(obj runtime.Object) error {
			ingress := obj.(*netv1.Ingress)
			ingressLeaseID, err := clientcommon.RecoverLeaseIDFromLabels(ingress.Labels)
			if err != nil {
				return err
			}
			if len(ingress.Spec.Rules) != 1 {
				return fmt.Errorf("%w: invalid number of rules %d", kubeclienterrors.ErrInvalidHostnameConnection, len(ingress.Spec.Rules))
			}
			rule := ingress.Spec.Rules[0]

			if len(rule.HTTP.Paths) != 1 {
				return fmt.Errorf("%w: invalid number of paths %d", kubeclienterrors.ErrInvalidHostnameConnection, len(rule.HTTP.Paths))
			}
			rulePath := rule.HTTP.Paths[0]
			results = append(results, chostname.LeaseIDHostnameConnection{
				LeaseID:      ingressLeaseID,
				Hostname:     rule.Host,
				ExternalPort: rulePath.Backend.Service.Port.Number,
				ServiceName:  rulePath.Backend.Service.Name,
			})

			return nil
		})

	if err != nil {
		return nil, err
	}

	return results, nil
}

func (op *hostnameOperator) getManifestGroup(ctx context.Context, lID mtypes.LeaseID) (bool, crd.ManifestGroup, error) {
	leaseNamespace := builder.LidNS(lID)

	obj, err := op.ac.AkashV2beta2().Manifests(op.ns).Get(ctx, leaseNamespace, metav1.GetOptions{})

	if err != nil {
		if kerrors.IsNotFound(err) {
			op.log.Info("CRD manifest not found", "lease-ns", leaseNamespace)
			return false, crd.ManifestGroup{}, nil
		}

		return false, crd.ManifestGroup{}, err
	}

	return true, obj.Spec.Group, nil
}

func (op *hostnameOperator) removeHostnameFromDeployment(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	if op.ingressConfig.IngressMode == builder.IngressModeGateway {
		return op.removeHostnameFromDeploymentGateway(ctx, hostname, leaseID, allowMissing)
	}

	ns := builder.LidNS(leaseID)
	labelSelector := &strings.Builder{}

	kubeSelectorForLease(labelSelector, leaseID)

	fieldSelector := &strings.Builder{}
	_, _ = fmt.Fprintf(fieldSelector, "metadata.name=%s", hostname)

	// This delete only works if the ingress exists & the labels match the lease ID given
	err := op.kc.NetworkingV1().Ingresses(ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		TypeMeta:             metav1.TypeMeta{},
		LabelSelector:        labelSelector.String(),
		FieldSelector:        fieldSelector.String(),
		Watch:                false,
		AllowWatchBookmarks:  false,
		ResourceVersion:      "",
		ResourceVersionMatch: "",
		TimeoutSeconds:       nil,
		Limit:                0,
		Continue:             "",
	})

	if err != nil && allowMissing && kerrors.IsNotFound(err) {
		return nil
	}

	return err
}

func (op *hostnameOperator) connectHostnameToDeployment(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	op.log.Info("hostname operator connecting hostname to deployment",
		"hostname", directive.Hostname,
		"ingress-mode", op.ingressConfig.IngressMode)

	if op.ingressConfig.IngressMode == builder.IngressModeGateway {
		op.log.Info("hostname operator using Gateway API mode")
		return op.connectHostnameToDeploymentGateway(ctx, directive)
	}

	op.log.Info("hostname operator using NGINX Ingress mode")
	ingressName := directive.Hostname
	ns := builder.LidNS(directive.LeaseID)
	rules := ingressRules(directive.Hostname, directive.ServiceName, directive.ServicePort)

	foundEntry, err := op.kc.NetworkingV1().Ingresses(ns).Get(ctx, ingressName, metav1.GetOptions{})
	// metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "ingresses-get", err, kubeErrors.IsNotFound)

	labels := make(map[string]string)
	labels[builder.AkashManagedLabelName] = "true"
	builder.AppendLeaseLabels(directive.LeaseID, labels)

	ingressClassName := akashIngressClassName
	obj := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ingressName,
			Labels:      labels,
			Annotations: kubeNginxIngressAnnotations(directive),
		},
		Spec: netv1.IngressSpec{
			IngressClassName: &ingressClassName,
			Rules:            rules,
		},
	}

	switch {
	case err == nil:
		obj.ResourceVersion = foundEntry.ResourceVersion
		_, err = op.kc.NetworkingV1().Ingresses(ns).Update(ctx, obj, metav1.UpdateOptions{})
		// metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "networking-ingresses-update", err)
	case kerrors.IsNotFound(err):
		_, err = op.kc.NetworkingV1().Ingresses(ns).Create(ctx, obj, metav1.CreateOptions{})
		// metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "networking-ingresses-create", err)
	}

	return err
}

func ingressRules(hostname string, kubeServiceName string, kubeServicePort int32) []netv1.IngressRule {
	// for some reason we need to pass a pointer to this
	pathTypeForAll := netv1.PathTypePrefix
	ruleValue := netv1.HTTPIngressRuleValue{
		Paths: []netv1.HTTPIngressPath{{
			Path:     "/",
			PathType: &pathTypeForAll,
			Backend: netv1.IngressBackend{
				Service: &netv1.IngressServiceBackend{
					Name: kubeServiceName,
					Port: netv1.ServiceBackendPort{
						Number: kubeServicePort,
					},
				},
			},
		}},
	}

	return []netv1.IngressRule{{
		Host:             hostname,
		IngressRuleValue: netv1.IngressRuleValue{HTTP: &ruleValue},
	}}
}

func kubeNginxIngressAnnotations(directive chostname.ConnectToDeploymentDirective) map[string]string {
	// For kubernetes/ingress-nginx
	// https://github.com/kubernetes/ingress-nginx
	const root = "nginx.ingress.kubernetes.io"

	readTimeout := math.Ceil(float64(directive.ReadTimeout) / 1000.0)
	sendTimeout := math.Ceil(float64(directive.SendTimeout) / 1000.0)
	result := map[string]string{
		fmt.Sprintf("%s/proxy-read-timeout", root): fmt.Sprintf("%d", int(readTimeout)),
		fmt.Sprintf("%s/proxy-send-timeout", root): fmt.Sprintf("%d", int(sendTimeout)),

		fmt.Sprintf("%s/proxy-next-upstream-tries", root): strconv.Itoa(int(directive.NextTries)),
		fmt.Sprintf("%s/proxy-body-size", root):           strconv.Itoa(int(directive.MaxBodySize)),
	}

	nextTimeoutKey := fmt.Sprintf("%s/proxy-next-upstream-timeout", root)
	nextTimeout := 0 // default magic value for disable
	if directive.NextTimeout > 0 {
		nextTimeout = int(math.Ceil(float64(directive.NextTimeout) / 1000.0))
	}

	result[nextTimeoutKey] = fmt.Sprintf("%d", nextTimeout)

	strBuilder := strings.Builder{}

	for i, v := range directive.NextCases {
		first := string(v[0])
		isHTTPCode := strings.ContainsAny(first, "12345")

		if isHTTPCode {
			strBuilder.WriteString("http_")
		}
		strBuilder.WriteString(v)

		if i != len(directive.NextCases)-1 {
			// The actual separator is the space character for kubernetes/ingress-nginx
			strBuilder.WriteRune(' ')
		}
	}

	result[fmt.Sprintf("%s/proxy-next-upstream", root)] = strBuilder.String()
	return result
}

func kubeSelectorForLease(dst *strings.Builder, lID mtypes.LeaseID) {
	_, _ = fmt.Fprintf(dst, "%s=%s", builder.AkashLeaseOwnerLabelName, lID.Owner)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseDSeqLabelName, lID.DSeq)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseGSeqLabelName, lID.GSeq)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseOSeqLabelName, lID.OSeq)
}

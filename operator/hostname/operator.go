package hostname

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"

	"cosmossdk.io/log"
	manifest "pkg.akt.dev/go/manifest/v2beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
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
	ctx                context.Context
	hostnames          map[string]managedHostname
	leasesIgnored      common.IgnoreList
	ns                 string
	log                log.Logger
	kc                 kubernetes.Interface
	ac                 akashclientset.Interface
	dc                 dynamic.Interface
	cfg                common.OperatorConfig
	server             common.OperatorHTTP
	flagHostnamesData  common.PrepareFlagFn
	flagIgnoreListData common.PrepareFlagFn
	gatewayName        string
	gatewayNamespace   string
	gatewayImpl        gateway.GatewayProvider
}

func newHostnameOperator(ctx context.Context, logger log.Logger, ns string, config common.OperatorConfig, ilc common.IgnoreListConfig) (*hostnameOperator, error) {
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

	// Resolved once at startup: with "auto", the operator picks up a Gateway class
	// switch on its next restart.
	gatewayImpl, err := gateway.ResolveProvider(ctx, dc, gwCfg.Provider, gwCfg.Namespace, gwCfg.Name, logger)
	if err != nil {
		return nil, fmt.Errorf("hostname operator: failed to resolve gateway provider: %w", err)
	}

	logger.Info("initializing hostname operator",
		"gateway_name", gwCfg.Name,
		"gateway_namespace", gwCfg.Namespace,
		"gateway_provider", gwCfg.Provider,
		"gateway_implementation", gatewayImpl.Name())

	op := &hostnameOperator{
		ctx:              ctx,
		hostnames:        make(map[string]managedHostname),
		ns:               ns,
		log:              logger,
		kc:               kc,
		ac:               ac,
		dc:               dc,
		cfg:              config,
		server:           opHTTP,
		leasesIgnored:    common.NewIgnoreList(ilc),
		gatewayName:      gwCfg.Name,
		gatewayNamespace: gwCfg.Namespace,
		gatewayImpl:      gatewayImpl,
	}

	op.flagIgnoreListData = op.server.AddPreparedEndpoint("/ignore-list", op.prepareIgnoreListData)
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
		op.log.Error("unable to get connections", "err", err)
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
		op.log.Debug("identified existing hostname connection",
			"hostname", hostname,
			"lid", entry.presentLease,
			"service", entry.presentServiceName,
			"port", entry.presentExternalPort)
	}
	op.flagHostnamesData()

	events, err := op.observeHostnameState(ctx)
	if err != nil {
		return err
	}

	retries := newHostnameRetries(ctx, hostnameRetryBase, hostnameRetryMax)
	defer retries.stop()

	pruneTicker := time.NewTicker(op.cfg.PruneInterval)
	defer pruneTicker.Stop()
	prepareTicker := time.NewTicker(op.cfg.WebRefreshInterval)
	defer prepareTicker.Stop()

	var exitError error
loop:
	for {
		select {
		case <-ctx.Done():
			exitError = ctx.Err()
			break loop
		case ev, ok := <-events:
			if !ok {
				exitError = common.ErrObservationStopped
				break loop
			}
			// a newer event supersedes any pending retry of the same hostname
			retries.forget(ev.GetHostname())
			if err = op.handleEvent(ctx, ev, retries); err != nil {
				exitError = err
				break loop
			}
		case tok := <-retries.due:
			ev, ok := retries.take(tok)
			if !ok {
				continue
			}
			if err = op.handleEvent(ctx, ev, retries); err != nil {
				exitError = err
				break loop
			}
		case <-pruneTicker.C:
			op.prune()
		case <-prepareTicker.C:
			if err := op.server.PrepareAll(); err != nil {
				op.log.Error("preparing web data failed", "err", err)
			}

		}
	}

	op.log.Debug("hostname operator done")

	return exitError
}

func (op *hostnameOperator) prepareIgnoreListData(pd common.PreparedResult) error {
	op.log.Debug("preparing ignore-list")
	return op.leasesIgnored.Prepare(pd)
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

func (op *hostnameOperator) prune() {
	if op.leasesIgnored.Prune() {
		op.flagIgnoreListData()
	}
}

func errorIsKubernetesResourceNotFound(failure error) bool {
	// check the error, only consider errors that are obviously
	// indicating a missing resource
	// otherwise simple errors like network issues could wind up with all CRDs
	// being ignored

	if kerrors.IsNotFound(failure) {
		return true
	}

	if errors.Is(failure, errExpectedResourceNotFound) {
		return true
	}

	errStr := failure.Error()
	// unless the error indicates a resource was not found, no action
	return strings.Contains(errStr, "not found")
}

func (op *hostnameOperator) recordEventError(ev chostname.ResourceEvent, failure error) {
	// no error, no action
	if failure == nil {
		return
	}

	mark := errorIsKubernetesResourceNotFound(failure)

	if !mark {
		return
	}

	op.log.Info("recording error for", "lid", ev.GetLeaseID(), "err", failure)

	op.leasesIgnored.AddError(ev.GetLeaseID(), failure, ev.GetHostname())
	op.flagIgnoreListData()
}

func (op *hostnameOperator) isEventIgnored(ev chostname.ResourceEvent) bool {
	return op.leasesIgnored.IsFlagged(ev.GetLeaseID())
}

// handleEvent applies an event. A failed add or update is rescheduled for that
// hostname alone; only other failures end the observation loop.
func (op *hostnameOperator) handleEvent(ctx context.Context, ev chostname.ResourceEvent, retries *hostnameRetries) error {
	err := op.applyEvent(ctx, ev)
	if err == nil {
		retries.forget(ev.GetHostname())
		return nil
	}

	switch ev.GetEventType() {
	case ctypes.ProviderResourceAdd, ctypes.ProviderResourceUpdate:
		delay := retries.schedule(ev)
		op.log.Error("failed applying event, will retry",
			"lid", ev.GetLeaseID(),
			"hostname", ev.GetHostname(),
			"retry_in", delay,
			"err", err)
		return nil
	default:
		op.log.Error("failed applying event", "lid", ev.GetLeaseID(), "err", err)
		return err
	}
}

func (op *hostnameOperator) applyEvent(ctx context.Context, ev chostname.ResourceEvent) error {
	log := op.log.With("lid", ev.GetLeaseID())

	log.Debug("apply event", "event_type", ev.GetEventType(), "hostname", ev.GetHostname())
	switch ev.GetEventType() {
	case ctypes.ProviderResourceDelete:
		// note that on delete the resource might be gone anyways because the namespace is deleted
		return op.applyDeleteEvent(ctx, ev)
	case ctypes.ProviderResourceAdd, ctypes.ProviderResourceUpdate:
		if op.isEventIgnored(ev) {
			log.Info("ignoring event")
			return nil
		}
		err := op.applyAddOrUpdateEvent(ctx, ev)
		op.recordEventError(ev, err)
		return err
	default:
		return fmt.Errorf("%w: unknown event type %v", common.ErrObservationStopped, ev.GetEventType())
	}

}

func (op *hostnameOperator) applyDeleteEvent(ctx context.Context, ev chostname.ResourceEvent) error {
	leaseID := ev.GetLeaseID()
	err := op.removeHostnameFromDeployment(ctx, ev.GetHostname(), leaseID, true)

	if err == nil {
		delete(op.hostnames, ev.GetHostname())
		op.flagHostnamesData()
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
		directive.ProxyBufferingDisable = serviceExpose.HTTPOptions.ProxyBufferingDisable
		directive.ProxyBufferSize = serviceExpose.HTTPOptions.ProxyBufferSize
		directive.ProxyBuffersNumber = serviceExpose.HTTPOptions.ProxyBuffersNumber
		directive.ProxyBuffersSize = serviceExpose.HTTPOptions.ProxyBuffersSize
		directive.ProxyBusyBuffersSize = serviceExpose.HTTPOptions.ProxyBusyBuffersSize
		directive.ProxyConnectTimeout = serviceExpose.HTTPOptions.ProxyConnectTimeout
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
	log := op.log.With("lid", leaseID)

	log.Debug("connecting",
		"hostname", ev.GetHostname(),
		"service", ev.GetServiceName(),
		"external_port", ev.GetExternalPort())
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
			log.Debug("hostname target is new, applying")
			// Check to see if port or service name is different
		} else if entry.presentExternalPort != ev.GetExternalPort() || entry.presentServiceName != ev.GetServiceName() {
			// shouldConnect = true
			log.Debug("hostname target has changed, applying")
		}

		// if shouldConnect {
		log.Debug("Updating ingress")
		// Update or create the existing ingress
		err = op.connectHostnameToDeployment(ctx, directive)
		// }
	} else {
		log.Debug("Swapping route to new deployment")
		// Delete the route in one namespace and recreate it in the correct one. A route
		// already gone must not block the transfer.
		err = op.removeHostnameFromDeployment(ctx, ev.GetHostname(), entry.presentLease, true)
		if err == nil {
			// Remove the current entry, if the next action succeeds then it gets inserted below
			delete(op.hostnames, ev.GetHostname())
			err = op.connectHostnameToDeployment(ctx, directive)
		}
	}

	record := func() {
		entry.presentExternalPort = ev.GetExternalPort()
		entry.presentServiceName = ev.GetServiceName()
		entry.presentLease = leaseID
		entry.lastEvent = ev
		entry.lastChangeAt = time.Now()
		op.hostnames[ev.GetHostname()] = entry
		op.flagHostnamesData()
	}

	if err == nil { // Update stored entry if everything went OK
		record()
		return nil
	}

	// A failed reconcile can still leave the route live: with route-first ordering
	// (Envoy) the routable route is published before its policies are applied and
	// accepted. Record ownership whenever the route exists, so a later transfer of the
	// hostname deletes it instead of leaving it serving this lease.
	if published, gErr := op.hostnameRouteExists(ctx, ev.GetHostname(), leaseID); gErr == nil && published {
		record()
	}

	return err
}

func (op *hostnameOperator) getHostnameDeploymentConnections(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	return op.getHostnameDeploymentConnectionsGateway(ctx)
}

func (op *hostnameOperator) observeHostnameState(ctx context.Context) (<-chan chostname.ResourceEvent, error) {
	phpager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		resources, err := op.ac.AkashV2beta2().ProviderHosts(op.ns).List(ctx, opts)
		return resources, err
	})

	data := make([]crd.ProviderHost, 0, 128)
	err := phpager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		ph := obj.(*crd.ProviderHost)
		data = append(data, *ph)
		return nil
	})

	if err != nil {
		return nil, err
	}

	op.log.Info("starting hostname watch")
	watcher, err := op.ac.AkashV2beta2().ProviderHosts(op.ns).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// a resource whose lease ID cannot be built is skipped rather than emitted without a type
	evData := make([]hostnameResourceEvent, 0, len(data))
	for _, v := range data {
		lid, err := v.Spec.LeaseID()
		if err != nil {
			op.log.Error("failed to convert LeaseID from CRD resource", "hostname", v.Spec.Hostname, "err", err)
			continue
		}

		evData = append(evData, hostnameResourceEvent{
			lid:          lid,
			eventType:    ctypes.ProviderResourceAdd,
			hostname:     v.Spec.Hostname,
			serviceName:  v.Spec.ServiceName,
			externalPort: v.Spec.ExternalPort,
		})
	}

	data = nil

	output := make(chan chostname.ResourceEvent)

	// every send also waits on ctx: the consumer stops reading when it fails to apply an event,
	// and cancels ctx when it returns
	go func() {
		defer func() {
			close(output)
			watcher.Stop()
		}()

		for _, v := range evData {
			select {
			case output <- v:
			case <-ctx.Done():
				return
			}
		}
		evData = nil // do not hold the reference

		results := watcher.ResultChan()
		for {
			select {
			case result, ok := <-results:
				if !ok { // Channel closed when an error happens
					return
				}

				// an error event carries a *metav1.Status; end the stream so the
				// observation restarts with a new list and watch
				if result.Type == watch.Error {
					op.log.Error("watch error", "err", kerrors.FromObject(result.Object))
					return
				}

				ph, ok := result.Object.(*crd.ProviderHost)
				if !ok {
					op.log.Error("unexpected object in hostname watch", "err", fmt.Errorf("unexpected object type %T", result.Object))
					continue
				}

				lid, err := ph.Spec.LeaseID()
				if err != nil {
					op.log.Error("failed to convert LeaseID from CRD resource", "hostname", ph.Spec.Hostname, "err", err)
					continue
				}

				ev := hostnameResourceEvent{
					lid:          lid,
					hostname:     ph.Spec.Hostname,
					serviceName:  ph.Spec.ServiceName,
					externalPort: ph.Spec.ExternalPort,
				}
				switch result.Type {

				case watch.Added:
					ev.eventType = ctypes.ProviderResourceAdd
				case watch.Modified:
					ev.eventType = ctypes.ProviderResourceUpdate
				case watch.Deleted:
					ev.eventType = ctypes.ProviderResourceDelete

				default:

					continue
				}

				select {
				case output <- ev:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return output, nil
}

func (op *hostnameOperator) getManifestGroup(ctx context.Context, lID mtypes.LeaseID) (bool, crd.ManifestGroup, error) {
	log := op.log.With("lid", lID)

	leaseNamespace := builder.LidNS(lID)

	obj, err := op.ac.AkashV2beta2().Manifests(op.ns).Get(ctx, leaseNamespace, metav1.GetOptions{})

	if err != nil {
		if kerrors.IsNotFound(err) {
			log.Info("CRD manifest not found", "ns", leaseNamespace)
			return false, crd.ManifestGroup{}, nil
		}

		return false, crd.ManifestGroup{}, err
	}

	return true, obj.Spec.Group, nil
}

func (op *hostnameOperator) removeHostnameFromDeployment(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	return op.removeHostnameFromDeploymentGateway(ctx, hostname, leaseID, allowMissing)
}

func (op *hostnameOperator) connectHostnameToDeployment(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	op.log.With("lid", directive.LeaseID).Info("hostname operator connecting hostname to deployment",
		"hostname", directive.Hostname,
		"gateway_implementation", op.gatewayImpl.Name())

	return op.connectHostnameToDeploymentGateway(ctx, directive)
}

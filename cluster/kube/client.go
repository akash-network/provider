package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"

	mapi "github.com/akash-network/akash-api/go/manifest/v2beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	apclient "github.com/akash-network/akash-api/go/provider/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/tendermint/tendermint/libs/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	netv1 "k8s.io/api/networking/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	"github.com/akash-network/node/sdl"
	metricsutils "github.com/akash-network/node/util/metrics"

	"github.com/akash-network/provider/cluster"
	"github.com/akash-network/provider/cluster/kube/builder"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashclient "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/fromctx"
)

var (
	kubeCallsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_kube_calls",
	}, []string{"action", "result"})
)

// Client interface includes cluster client
type Client interface {
	cluster.Client
}

var _ Client = (*client)(nil)

type client struct {
	ctx               context.Context
	kc                kubernetes.Interface
	ac                akashclient.Interface
	ns                string
	log               log.Logger
	kubeContentConfig *restclient.Config
}

func (c *client) String() string {
	return fmt.Sprintf("kube client %p ns=%s", c, c.ns)
}

func wrapKubeCall[T any](label string, fn func() (T, error)) (T, error) {
	res, err := fn()

	_ = "https://github.com/akash-network/provider/blob/main/cluster/kube/client.go#L70"

	status := metricsutils.SuccessLabel
	if err != nil {
		status = metricsutils.FailLabel
	}
	kubeCallsCounter.WithLabelValues(label, status).Inc()

	return res, err
}

// NewClient returns new Kubernetes Client instance with provided logger, host and ns. Returns error in-case of failure
// configPath may be the empty string
func NewClient(ctx context.Context, log log.Logger, ns string) (Client, error) {
	kubecfg, err := fromctx.KubeConfigFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	kc, err := fromctx.KubeClientFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	ac, err := fromctx.AkashClientFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	_, err = kc.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: unable to fetch leases namespace: %w", err)
	}

	cl := &client{
		ctx:               ctx,
		kc:                kc,
		ac:                ac,
		ns:                ns,
		log:               log.With("client", "kube"),
		kubeContentConfig: kubecfg,
	}

	return cl, nil
}

func (c *client) GetDeployment(ctx context.Context, dID dtypes.DeploymentID) ([]ctypes.IDeployment, error) {
	labelSelectors := &strings.Builder{}
	_, _ = fmt.Fprintf(labelSelectors, "%s=%d", builder.AkashLeaseDSeqLabelName, dID.DSeq)
	_, _ = fmt.Fprintf(labelSelectors, ",%s=%s", builder.AkashLeaseOwnerLabelName, dID.Owner)

	manifests, err := wrapKubeCall("manifests-list", func() (*crd.ManifestList, error) {
		return c.ac.AkashV2beta2().Manifests(c.ns).List(ctx, metav1.ListOptions{
			TypeMeta:             metav1.TypeMeta{},
			LabelSelector:        labelSelectors.String(),
			FieldSelector:        "",
			Watch:                false,
			AllowWatchBookmarks:  false,
			ResourceVersion:      "",
			ResourceVersionMatch: "",
			TimeoutSeconds:       nil,
			Limit:                0,
			Continue:             "",
		})
	})

	if err != nil {
		return nil, err
	}

	result := make([]ctypes.IDeployment, len(manifests.Items))
	for i, manifest := range manifests.Items {
		result[i], err = manifest.Deployment()
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (c *client) GetManifestGroup(ctx context.Context, lID mtypes.LeaseID) (bool, crd.ManifestGroup, error) {
	leaseNamespace := builder.LidNS(lID)

	obj, err := wrapKubeCall("manifests-get", func() (*crd.Manifest, error) {
		return c.ac.AkashV2beta2().Manifests(c.ns).Get(ctx, leaseNamespace, metav1.GetOptions{})
	})

	if err != nil {
		if kerrors.IsNotFound(err) {
			c.log.Info("CRD manifest not found", "lease-ns", leaseNamespace)
			return false, crd.ManifestGroup{}, nil
		}

		return false, crd.ManifestGroup{}, err
	}

	return true, obj.Spec.Group, nil
}

func (c *client) Deployments(ctx context.Context) ([]ctypes.IDeployment, error) {
	manifests, err := wrapKubeCall("manifests-list", func() (*crd.ManifestList, error) {
		return c.ac.AkashV2beta2().Manifests(c.ns).List(ctx, metav1.ListOptions{})
	})

	if err != nil {
		return nil, err
	}

	deployments := make([]ctypes.IDeployment, 0, len(manifests.Items))
	for _, manifest := range manifests.Items {
		deployment, err := manifest.Deployment()
		if err != nil {
			return deployments, err
		}
		deployments = append(deployments, deployment)
	}

	return deployments, nil
}

type deploymentService struct {
	deployment    builder.Deployment
	statefulSet   builder.StatefulSet
	localService  builder.Service
	globalService builder.Service
	credentials   builder.ServiceCredentials
}

type deploymentApplies struct {
	ns        builder.NS
	netPol    builder.NetPol
	cmanifest builder.Manifest
	services  []*deploymentService
}

type previousObj struct {
	nmani           *crd.Manifest
	umani           *crd.Manifest
	omani           *crd.Manifest
	nns             *corev1.Namespace
	uns             *corev1.Namespace
	ons             *corev1.Namespace
	nNetPolicies    []netv1.NetworkPolicy
	uNetPolicies    []netv1.NetworkPolicy
	oNetPolicies    []netv1.NetworkPolicy
	nServiceCreds   []*corev1.Secret
	uServiceCreds   []*corev1.Secret
	oServiceCreds   []*corev1.Secret
	nStatefulSets   []*appsv1.StatefulSet
	uStatefulSets   []*appsv1.StatefulSet
	oStatefulSets   []*appsv1.StatefulSet
	nDeployments    []*appsv1.Deployment
	uDeployments    []*appsv1.Deployment
	oDeployments    []*appsv1.Deployment
	nLocalServices  []*corev1.Service
	uLocalServices  []*corev1.Service
	oLocalServices  []*corev1.Service
	nGlobalServices []*corev1.Service
	uGlobalServices []*corev1.Service
	oGlobalServices []*corev1.Service
}

func (p *previousObj) recover(ctx context.Context, kc kubernetes.Interface, ac akashclient.Interface) []error {
	var errs []error

	for _, val := range slices.Backward(p.nGlobalServices) {
		if err := kc.CoreV1().Services(val.Namespace).Delete(ctx, val.Name, metav1.DeleteOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.oGlobalServices) {
		if _, err := kc.CoreV1().Services(val.Namespace).Update(ctx, val, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.nLocalServices) {
		if err := kc.CoreV1().Services(val.Namespace).Delete(ctx, val.Name, metav1.DeleteOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.oLocalServices) {
		if _, err := kc.CoreV1().Services(val.Namespace).Update(ctx, val, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.oDeployments) {
		if _, err := kc.AppsV1().Deployments(val.Namespace).Update(ctx, val, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.oStatefulSets) {
		if _, err := kc.AppsV1().StatefulSets(val.Namespace).Update(ctx, val, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.nServiceCreds) {
		if err := kc.CoreV1().Secrets(val.Namespace).Delete(ctx, val.Name, metav1.DeleteOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.oServiceCreds) {
		if _, err := kc.CoreV1().Secrets(val.Namespace).Update(ctx, val, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.nNetPolicies) {
		if err := kc.NetworkingV1().NetworkPolicies(val.Namespace).Delete(ctx, val.Name, metav1.DeleteOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	for _, val := range slices.Backward(p.oNetPolicies) {
		if _, err := kc.NetworkingV1().NetworkPolicies(val.Namespace).Update(ctx, &val, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	if p.nns != nil {
		if err := kc.CoreV1().Namespaces().Delete(ctx, p.nns.Name, metav1.DeleteOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	if p.ons != nil {
		if _, err := kc.CoreV1().Namespaces().Update(ctx, p.ons, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	if p.omani != nil {
		if _, err := ac.AkashV2beta2().Manifests(p.omani.Namespace).Update(ctx, p.omani, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

type deployObjNames struct {
	deployments  map[string]string
	statefulSets map[string]string
	services     map[string]string
}

func (c *client) Deploy(ctx context.Context, deployment ctypes.IDeployment) (err error) {
	var settings builder.Settings
	var valid bool

	if settings, valid = ctx.Value(builder.SettingsKey).(builder.Settings); !valid {
		err = kubeclienterrors.ErrNotConfiguredWithSettings
		return
	}

	if err = builder.ValidateSettings(settings); err != nil {
		return
	}

	var cdeployment builder.IClusterDeployment

	if cdeployment, err = builder.ClusterDeploymentFromDeployment(deployment); err != nil {
		if cdeployment != nil {
			tMani := builder.BuildManifest(c.log, settings, c.ns, cdeployment)

			if cr, er := tMani.Create(); er == nil {
				data, _ := json.Marshal(cr)
				c.log.Error(fmt.Sprintf("debug manifest %s", string(data)))
			}
		}

		return
	}

	lid := cdeployment.LeaseID()
	group := cdeployment.ManifestGroup()

	po := &previousObj{}

	defer func() {
		tmpErr := err

		if recover() != nil {
			c.log.Error(fmt.Sprintf("recovered from panic: \n%s", string(debug.Stack())))
			err = kubeclienterrors.ErrInternalError
		}

		if tmpErr != nil || err != nil {
			var dArgs []any
			var dMsg string

			applyMsgLog := func(msg string, arg any) {
				dMsg += msg
				dArgs = append(dArgs, arg)
			}

			applyMsgLog("unable to deploy lid=%s. last known state:\n", lid)

			c.log.Error(fmt.Sprintf(dMsg, dArgs...))

			c.log.Info("attempting recover objects to previous state")
			po.recover(ctx, c.kc, c.ac)
		}
	}()

	applies := deploymentApplies{
		services: make([]*deploymentService, 0, len(group.Services)),
	}

	applies.cmanifest = builder.BuildManifest(c.log, settings, c.ns, cdeployment)

	po.omani, err = c.ac.AkashV2beta2().Manifests(c.ns).Get(ctx, applies.cmanifest.Name(), metav1.GetOptions{})
	metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "akash-manifests-get", err, kerrors.IsNotFound)

	if err != nil && !kerrors.IsNotFound(err) {
		return err
	} else if kerrors.IsNotFound(err) {
		po.omani = nil
	}

	// at this moment send manifest REST handle cannot validate new manifest against existing services.
	// this part ensures tenant cannot rename or add/delete services in the manifest after creating deployment
	if cdeployment.UpdateManifest() {
		if po.omani != nil {
			po.umani, err = applies.cmanifest.Update(po.omani)
		} else {
			po.nmani, err = applies.cmanifest.Create()
		}

		if err != nil {
			return err
		}

		currSvcs := make(map[string]*crd.ManifestService)

		if po.omani != nil {
			for _, svc := range po.omani.Spec.Group.Services {
				currSvcs[svc.Name] = &svc
			}
		}

		if po.umani != nil {
			for _, svc := range po.umani.Spec.Group.Services {
				if _, exists := currSvcs[svc.Name]; !exists {
					return fmt.Errorf("service %s not found: %w", svc.Name, builder.ErrManifestRenameNotAllowed)
				}
				delete(currSvcs, svc.Name)
			}
		}

		if len(currSvcs) > 0 {
			return fmt.Errorf("manifest does not match service names with existing version: %w", builder.ErrManifestRenameNotAllowed)
		}

		if po.omani != nil {
			if !reflect.DeepEqual(&po.umani.Spec, &po.omani.Spec) || !reflect.DeepEqual(po.umani.Labels, po.omani.Labels) {
				po.umani, err = c.ac.AkashV2beta2().Manifests(applies.cmanifest.NS()).Update(ctx, po.umani, metav1.UpdateOptions{})
				metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "akash-manifests-update", err)
			}
		} else {
			po.nmani, err = c.ac.AkashV2beta2().Manifests(applies.cmanifest.NS()).Create(ctx, po.nmani, metav1.CreateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "akash-manifests-create", err)
		}

		if err != nil {
			c.log.Error("applying manifest", "err", err, "lease", lid)
			return err
		}
	}

	var currManifest *crd.Manifest

	if po.nmani != nil {
		currManifest = po.nmani
	} else if po.umani != nil {
		currManifest = po.umani
	} else {
		currManifest = po.omani
	}

	if cdeployment.GetResourceVersion() == "" {
		cdeployment.SetResourceVersion(currManifest.ResourceVersion)
	}

	applies.ns = builder.BuildNS(settings, cdeployment)
	applies.netPol = builder.BuildNetPol(settings, cdeployment)

	for svcIdx := range group.Services {
		workload, err := builder.NewWorkloadBuilder(c.log, settings, cdeployment, currManifest, svcIdx)
		if err != nil {
			return err
		}

		service := &group.Services[svcIdx]

		svc := &deploymentService{}

		if service.Credentials != nil {
			svc.credentials = builder.NewServiceCredentials(workload, service.Credentials)
		}

		persistent := false
		for i := range service.Resources.Storage {
			attrVal := service.Resources.Storage[i].Attributes.Find(sdl.StorageAttributePersistent)
			if persistent, _ = attrVal.AsBool(); persistent {
				break
			}
		}

		if persistent {
			svc.statefulSet = builder.BuildStatefulSet(workload)
		} else {
			svc.deployment = builder.NewDeployment(workload)
		}

		applies.services = append(applies.services, svc)

		if len(service.Expose) == 0 {
			c.log.Debug("lease does not have services (no expose configuration provided)", "lease", lid, "service", service.Name)
			continue
		}

		svc.localService = builder.BuildService(workload, false)
		svc.globalService = builder.BuildService(workload, true)
	}

	po.nns, po.uns, po.ons, err = applyNS(ctx, c.kc, applies.ns)
	if err != nil {
		c.log.Error("applying namespace", "err", err, "lease", lid)
		return err
	}

	po.nNetPolicies, po.uNetPolicies, po.oNetPolicies, err = applyNetPolicies(ctx, c.kc, applies.netPol)
	if err != nil {
		c.log.Error("applying namespace network policies", "err", err, "lease", lid)
		return err
	}

	if err = cleanupStaleResources(ctx, c.kc, lid, group); err != nil {
		c.log.Error("cleaning stale resources", "err", err, "lease", lid)
		return err
	}

	objNames := deployObjNames{
		deployments:  make(map[string]string),
		statefulSets: make(map[string]string),
		services:     make(map[string]string),
	}

	cSvcs, err := c.kc.CoreV1().Services(po.ons.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	cDepls, err := c.kc.AppsV1().Deployments(po.ons.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	cStats, err := c.kc.AppsV1().StatefulSets(po.ons.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, obj := range cSvcs.Items {
		objNames.services[obj.Name] = obj.ResourceVersion
	}

	for _, obj := range cDepls.Items {
		objNames.deployments[obj.Name] = obj.ResourceVersion
	}

	for _, obj := range cStats.Items {
		objNames.statefulSets[obj.Name] = obj.ResourceVersion
	}

	for svcIdx := range group.Services {
		applyObjs := applies.services[svcIdx]
		service := &group.Services[svcIdx]

		if applyObjs.credentials != nil {
			nobj, uobj, obj, err := applyServiceCredentials(ctx, c.kc, applyObjs.credentials)
			if err != nil {
				c.log.Error("applying credentials", "err", err, "lease", lid, "service", service.Name)
				return err
			}

			if nobj != nil {
				po.nServiceCreds = append(po.nServiceCreds, nobj)
			}
			if uobj != nil {
				po.uServiceCreds = append(po.uServiceCreds, uobj)
			}
			if obj != nil {
				po.oServiceCreds = append(po.oServiceCreds, obj)
			}
		}

		if applyObjs.statefulSet != nil {
			nobj, uobj, oobj, err := applyStatefulSet(ctx, c.kc, applyObjs.statefulSet)
			if err != nil {
				c.log.Error("applying statefulSet", "err", err, "lease", lid, "service", service.Name)
				return err
			}

			if nobj != nil {
				po.nStatefulSets = append(po.nStatefulSets, nobj)
			}
			if uobj != nil {
				po.uStatefulSets = append(po.uStatefulSets, uobj)
			}
			if oobj != nil {
				po.oStatefulSets = append(po.oStatefulSets, oobj)
			}
		}

		if applyObjs.deployment != nil {
			nobj, uobj, oobj, err := applyDeployment(ctx, c.kc, applyObjs.deployment)
			if err != nil {
				c.log.Error("applying deployment", "err", err, "lease", lid, "service", service.Name)
				return err
			}

			if nobj != nil {
				po.nDeployments = append(po.nDeployments, nobj)
			}
			if uobj != nil {
				po.uDeployments = append(po.uDeployments, uobj)
			}
			if oobj != nil {
				po.oDeployments = append(po.oDeployments, oobj)
			}
		}

		if lsvc := applyObjs.localService; lsvc != nil && lsvc.Any() {
			nobj, uobj, oobj, err := applyService(ctx, c.kc, lsvc)
			if err != nil {
				c.log.Error("applying local service", "err", err, "lease", lid, "service", service.Name)
				return err
			}

			if nobj != nil {
				po.nLocalServices = append(po.nLocalServices, nobj)
			}
			if uobj != nil {
				po.uLocalServices = append(po.uLocalServices, uobj)
			}
			if oobj != nil {
				po.oLocalServices = append(po.oLocalServices, oobj)
			}
		}

		if gsvc := applyObjs.globalService; gsvc != nil && gsvc.Any() {
			nobj, uobj, oobj, err := applyService(ctx, c.kc, gsvc)
			if err != nil {
				c.log.Error("applying global service", "err", err, "lease", lid, "service", service.Name)
				return err
			}

			if nobj != nil {
				po.nGlobalServices = append(po.nGlobalServices, nobj)
			}
			if uobj != nil {
				po.uGlobalServices = append(po.uGlobalServices, uobj)
			}
			if oobj != nil {
				po.oGlobalServices = append(po.oGlobalServices, oobj)
			}
		}
	}

	return nil
}

func (c *client) TeardownLease(ctx context.Context, lid mtypes.LeaseID) error {
	c.log.Info("tearing down lease", "lease", lid)

	_, result := wrapKubeCall("namespaces-delete", func() (interface{}, error) {
		return nil, c.kc.CoreV1().Namespaces().Delete(ctx, builder.LidNS(lid), metav1.DeleteOptions{})
	})

	if result != nil {
		c.log.Error("teardown lease: unable to delete namespace", "ns", builder.LidNS(lid), "error", result)
		if kerrors.IsNotFound(result) {
			result = nil
		}
	}
	_, err := wrapKubeCall("manifests-delete", func() (interface{}, error) {
		return nil, c.ac.AkashV2beta2().Manifests(c.ns).Delete(ctx, builder.LidNS(lid), metav1.DeleteOptions{})
	})

	if err != nil {
		c.log.Error("teardown lease: unable to delete manifest", "ns", builder.LidNS(lid), "error", err)
	}

	return result
}

func kubeSelectorForLease(dst *strings.Builder, lID mtypes.LeaseID) {
	_, _ = fmt.Fprintf(dst, "%s=%s", builder.AkashLeaseOwnerLabelName, lID.Owner)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseDSeqLabelName, lID.DSeq)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseGSeqLabelName, lID.GSeq)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseOSeqLabelName, lID.OSeq)
}

func newEventsFeedList(ctx context.Context, events []eventsv1.Event) ctypes.EventsWatcher {
	wtch := ctypes.NewEventsFeed(ctx)

	go func() {
		defer wtch.Shutdown()

	done:
		for _, evt := range events {
			if !wtch.SendEvent(&evt) {
				break done
			}
		}
	}()

	return wtch
}

func newEventsFeedWatch(ctx context.Context, events watch.Interface) ctypes.EventsWatcher {
	wtch := ctypes.NewEventsFeed(ctx)

	go func() {
		defer func() {
			events.Stop()
			wtch.Shutdown()
		}()

	done:
		for {
			select {
			case obj, ok := <-events.ResultChan():
				if !ok {
					break done
				}
				evt := obj.Object.(*eventsv1.Event)
				if !wtch.SendEvent(evt) {
					break done
				}
			case <-wtch.Done():
				break done
			}
		}
	}()

	return wtch
}

func (c *client) LeaseEvents(ctx context.Context, lid mtypes.LeaseID, services string, follow bool) (ctypes.EventsWatcher, error) {
	if err := c.leaseExists(ctx, lid); err != nil {
		return nil, err
	}

	listOpts := metav1.ListOptions{}
	if len(services) != 0 {
		listOpts.LabelSelector = fmt.Sprintf(builder.AkashManifestServiceLabelName+" in (%s)", services)
	}

	var wtch ctypes.EventsWatcher
	if follow {
		watcher, err := wrapKubeCall("events-follow", func() (watch.Interface, error) {
			return c.kc.EventsV1().Events(builder.LidNS(lid)).Watch(ctx, listOpts)
		})

		if err != nil {
			return nil, err
		}

		wtch = newEventsFeedWatch(ctx, watcher)
	} else {
		list, err := wrapKubeCall("events-list", func() (*eventsv1.EventList, error) {
			return c.kc.EventsV1().Events(builder.LidNS(lid)).List(ctx, listOpts)
		})

		if err != nil {
			return nil, err
		}

		wtch = newEventsFeedList(ctx, list.Items)
	}

	return wtch, nil
}

func (c *client) LeaseLogs(ctx context.Context, lid mtypes.LeaseID,
	services string, follow bool, tailLines *int64) ([]*ctypes.ServiceLog, error) {
	if err := c.leaseExists(ctx, lid); err != nil {
		return nil, err
	}

	listOpts := metav1.ListOptions{}
	if len(services) != 0 {
		listOpts.LabelSelector = fmt.Sprintf(builder.AkashManifestServiceLabelName+" in (%s)", services)
	}

	c.log.Info("filtering pods", "labelSelector", listOpts.LabelSelector)

	pods, err := wrapKubeCall("pods-list", func() (*corev1.PodList, error) {
		return c.kc.CoreV1().Pods(builder.LidNS(lid)).List(ctx, listOpts)
	})
	if err != nil {
		c.log.Error("listing pods", "err", err)
		return nil, fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
	}
	streams := make([]*ctypes.ServiceLog, len(pods.Items))
	for i, pod := range pods.Items {
		stream, err := wrapKubeCall("pods-getlogs", func() (io.ReadCloser, error) {
			return c.kc.CoreV1().Pods(builder.LidNS(lid)).GetLogs(pod.Name, &corev1.PodLogOptions{
				Follow:     follow,
				TailLines:  tailLines,
				Timestamps: false,
			}).Stream(ctx)
		})

		if err != nil {
			c.log.Error("get pod logs", "err", err)
			return nil, fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
		}
		streams[i] = cluster.NewServiceLog(pod.Name, stream)
	}
	return streams, nil
}

func (c *client) ForwardedPortStatus(ctx context.Context, leaseID mtypes.LeaseID) (map[string][]apclient.ForwardedPortStatus, error) {
	settingsI := ctx.Value(builder.SettingsKey)
	if nil == settingsI {
		return nil, kubeclienterrors.ErrNotConfiguredWithSettings
	}
	settings := settingsI.(builder.Settings)
	if err := builder.ValidateSettings(settings); err != nil {
		return nil, err
	}

	services, err := wrapKubeCall("services-list", func() (*corev1.ServiceList, error) {
		return c.kc.CoreV1().Services(builder.LidNS(leaseID)).List(ctx, metav1.ListOptions{})
	})
	if err != nil {
		c.log.Error("list services", "err", err)
		return nil, fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
	}

	forwardedPorts := make(map[string][]apclient.ForwardedPortStatus)

	// Search for a Kubernetes service declared as nodeport
	for _, service := range services.Items {
		if service.Spec.Type == corev1.ServiceTypeNodePort {
			serviceName := service.Name // Always suffixed during creation, so chop it off
			deploymentName := serviceName[0 : len(serviceName)-len(builder.SuffixForNodePortServiceName)]

			if 0 != len(service.Spec.Ports) {
				portsForDeployment := make([]apclient.ForwardedPortStatus, 0, len(service.Spec.Ports))
				for _, port := range service.Spec.Ports {
					// Check if the service is exposed via NodePort mechanism in the cluster
					// This is a random port chosen by the cluster when the deployment is created
					nodePort := port.NodePort
					if nodePort > 0 {
						// Record the actual port inside the container that is exposed
						v := apclient.ForwardedPortStatus{
							Host:         settings.ClusterPublicHostname,
							Port:         uint16(port.TargetPort.IntVal), // nolint: gosec
							ExternalPort: uint16(nodePort),               // nolint: gosec
							Name:         deploymentName,
						}

						isValid := true
						switch port.Protocol {
						case corev1.ProtocolTCP:
							v.Proto = mapi.TCP
						case corev1.ProtocolUDP:
							v.Proto = mapi.UDP
						default:
							isValid = false // Skip this, since the Protocol is set to something not supported by Akash
						}
						if isValid {
							portsForDeployment = append(portsForDeployment, v)
						}
					}
				}
				forwardedPorts[deploymentName] = portsForDeployment
			}
		}
	}

	return forwardedPorts, nil
}

// LeaseStatus todo: limit number of results and do pagination / streaming
func (c *client) LeaseStatus(ctx context.Context, lid mtypes.LeaseID) (map[string]*apclient.ServiceStatus, error) {
	settingsI := ctx.Value(builder.SettingsKey)
	if nil == settingsI {
		return nil, kubeclienterrors.ErrNotConfiguredWithSettings
	}
	settings := settingsI.(builder.Settings)
	if err := builder.ValidateSettings(settings); err != nil {
		return nil, err
	}

	serviceStatus, err := c.deploymentsForLease(ctx, lid)
	if err != nil {
		return nil, err
	}
	labelSelector := &strings.Builder{}
	kubeSelectorForLease(labelSelector, lid)
	// Note: this is a separate call to the Kubernetes API to get this data. It could
	// be a separate method on the interface entirely
	phResult, err := wrapKubeCall("providerhosts-list", func() (*crd.ProviderHostList, error) {
		return c.ac.AkashV2beta2().ProviderHosts(c.ns).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector.String(),
		})
	})
	if err != nil {
		return nil, err
	}

	// For each provider host entry, update the status of each service to indicate
	// the presently assigned hostnames
	for _, ph := range phResult.Items {
		entry, ok := serviceStatus[ph.Spec.ServiceName]
		if ok {
			entry.URIs = append(entry.URIs, ph.Spec.Hostname)
		}
	}

	return serviceStatus, nil
}

func (c *client) ServiceStatus(ctx context.Context, lid mtypes.LeaseID, name string) (*apclient.ServiceStatus, error) {
	if err := c.leaseExists(ctx, lid); err != nil {
		return nil, err
	}

	// Get manifest definition from CRD
	mani, err := wrapKubeCall("manifests-list", func() (*crd.Manifest, error) {
		return c.ac.AkashV2beta2().Manifests(c.ns).Get(ctx, builder.LidNS(lid), metav1.GetOptions{})
	})

	if err != nil {
		c.log.Error("CRD manifest not found", "lease-ns", builder.LidNS(lid), "name", name)
		return nil, kubeclienterrors.ErrNoManifestForLease
	}

	var result *apclient.ServiceStatus

	var svc *crd.ManifestService

	for i, s := range mani.Spec.Group.Services {
		if s.Name == name {
			svc = &mani.Spec.Group.Services[i]
			break
		}
	}

	if svc == nil {
		return nil, kubeclienterrors.ErrNoServiceForLease
	}

	isDeployment := true
	if params := svc.Params; params != nil {
		for _, param := range params.Storage {
			if param.Mount != "" {
				isDeployment = false
				break
			}
		}
	}

	if isDeployment {
		c.log.Debug("get deployment", "lease-ns", builder.LidNS(lid), "name", name)
		deployment, err := wrapKubeCall("deployments-get", func() (*appsv1.Deployment, error) {
			return c.kc.AppsV1().Deployments(builder.LidNS(lid)).Get(ctx, name, metav1.GetOptions{})
		})

		if err != nil {
			c.log.Error("deployment get", "err", err)
			return nil, fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
		}
		if deployment == nil {
			c.log.Error("no deployment found", "name", name)
			return nil, kubeclienterrors.ErrNoDeploymentForLease
		}

		result = &apclient.ServiceStatus{
			Name:               deployment.Name,
			Available:          deployment.Status.AvailableReplicas,
			Total:              deployment.Status.Replicas,
			ObservedGeneration: deployment.Status.ObservedGeneration,
			Replicas:           deployment.Status.Replicas,
			UpdatedReplicas:    deployment.Status.UpdatedReplicas,
			ReadyReplicas:      deployment.Status.ReadyReplicas,
			AvailableReplicas:  deployment.Status.AvailableReplicas,
		}
	} else {
		c.log.Debug("get statefulsets", "lease-ns", builder.LidNS(lid), "name", name)
		statefulset, err := wrapKubeCall("statefulsets-get", func() (*appsv1.StatefulSet, error) {
			return c.kc.AppsV1().StatefulSets(builder.LidNS(lid)).Get(ctx, name, metav1.GetOptions{})
		})

		if err != nil {
			c.log.Error("statefulsets get", "err", err)
			return nil, fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
		}
		if statefulset == nil {
			c.log.Error("no statefulsets found", "name", name)
			return nil, kubeclienterrors.ErrNoDeploymentForLease
		}

		result = &apclient.ServiceStatus{
			Name:               statefulset.Name,
			Available:          statefulset.Status.CurrentReplicas,
			Total:              statefulset.Status.Replicas,
			ObservedGeneration: statefulset.Status.ObservedGeneration,
			Replicas:           statefulset.Status.Replicas,
			UpdatedReplicas:    statefulset.Status.UpdatedReplicas,
			ReadyReplicas:      statefulset.Status.ReadyReplicas,
			AvailableReplicas:  statefulset.Status.CurrentReplicas,
		}
	}

	hasHostnames := false

	found := false
exposeCheckLoop:
	for _, service := range mani.Spec.Group.Services {
		if service.Name == name {
			found = true
			for _, expose := range service.Expose {
				proto, err := mapi.ParseServiceProtocol(expose.Proto)
				if err != nil {
					return nil, err
				}
				mse := mapi.ServiceExpose{
					Port:         uint32(expose.Port),
					ExternalPort: uint32(expose.ExternalPort),
					Proto:        proto,
					Service:      expose.Service,
					Global:       expose.Global,
					Hosts:        expose.Hosts,
				}
				if mse.IsIngress() {
					hasHostnames = true
					break exposeCheckLoop
				}
			}
		}
	}

	if !found {
		return nil, fmt.Errorf("%w: service %q", kubeclienterrors.ErrNoServiceForLease, name)
	}

	c.log.Debug("service result", "lease-ns", builder.LidNS(lid), "has-hostnames", hasHostnames)

	if hasHostnames {
		labelSelector := &strings.Builder{}
		kubeSelectorForLease(labelSelector, lid)

		phs, err := wrapKubeCall("provider-hosts", func() (*crd.ProviderHostList, error) {
			return c.ac.AkashV2beta2().ProviderHosts(c.ns).List(ctx, metav1.ListOptions{
				LabelSelector: labelSelector.String(),
			})
		})

		if err != nil {
			c.log.Error("provider hosts get", "err", err)
			return nil, fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
		}

		hosts := make([]string, 0, len(phs.Items))
		for _, ph := range phs.Items {
			hosts = append(hosts, ph.Spec.Hostname)
		}

		result.URIs = hosts
	}

	return result, nil
}

func (c *client) leaseExists(ctx context.Context, lid mtypes.LeaseID) error {
	_, err := wrapKubeCall("namespace-get", func() (*corev1.Namespace, error) {
		return c.kc.CoreV1().Namespaces().Get(ctx, builder.LidNS(lid), metav1.GetOptions{})
	})

	if err != nil {
		if kerrors.IsNotFound(err) {
			return kubeclienterrors.ErrLeaseNotFound
		}

		c.log.Error("namespaces get", "err", err)
		return fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
	}

	return nil
}

func (c *client) deploymentsForLease(ctx context.Context, lid mtypes.LeaseID) (map[string]*apclient.ServiceStatus, error) {
	if err := c.leaseExists(ctx, lid); err != nil {
		return nil, err
	}

	deployments, err := wrapKubeCall("deployments-list", func() (*appsv1.DeploymentList, error) {
		return c.kc.AppsV1().Deployments(builder.LidNS(lid)).List(ctx, metav1.ListOptions{})
	})

	if err != nil {
		c.log.Error("deployments list", "err", err)
		return nil, fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
	}

	statefulsets, err := wrapKubeCall("statefulsets-list", func() (*appsv1.StatefulSetList, error) {
		return c.kc.AppsV1().StatefulSets(builder.LidNS(lid)).List(ctx, metav1.ListOptions{})
	})

	if err != nil {
		c.log.Error("statefulsets list", "err", err)
		return nil, fmt.Errorf("%s: %w", kubeclienterrors.ErrInternalError.Error(), err)
	}

	serviceStatus := make(map[string]*apclient.ServiceStatus)

	if deployments != nil {
		for _, deployment := range deployments.Items {
			serviceStatus[deployment.Name] = &apclient.ServiceStatus{
				Name:               deployment.Name,
				Available:          deployment.Status.AvailableReplicas,
				Total:              deployment.Status.Replicas,
				ObservedGeneration: deployment.Status.ObservedGeneration,
				Replicas:           deployment.Status.Replicas,
				UpdatedReplicas:    deployment.Status.UpdatedReplicas,
				ReadyReplicas:      deployment.Status.ReadyReplicas,
				AvailableReplicas:  deployment.Status.AvailableReplicas,
			}
		}
	}

	if statefulsets != nil {
		for _, statefulset := range statefulsets.Items {
			serviceStatus[statefulset.Name] = &apclient.ServiceStatus{
				Name:               statefulset.Name,
				Available:          statefulset.Status.CurrentReplicas,
				Total:              statefulset.Status.Replicas,
				ObservedGeneration: statefulset.Status.ObservedGeneration,
				Replicas:           statefulset.Status.Replicas,
				UpdatedReplicas:    statefulset.Status.UpdatedReplicas,
				ReadyReplicas:      statefulset.Status.ReadyReplicas,
				AvailableReplicas:  statefulset.Status.CurrentReplicas,
			}
		}
	}

	if len(serviceStatus) == 0 {
		c.log.Info("No deployments found for", "lease namespace", builder.LidNS(lid))
		return nil, kubeclienterrors.ErrNoDeploymentForLease
	}

	return serviceStatus, nil
}

func (c *client) KubeVersion() (*version.Info, error) {
	return wrapKubeCall("discovery-serverversion", func() (*version.Info, error) {
		return c.kc.Discovery().ServerVersion()
	})
}

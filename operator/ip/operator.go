package ip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gorilla/mux"
	"github.com/tendermint/tendermint/libs/log"
	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"

	sdktypes "github.com/cosmos/cosmos-sdk/types"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"

	"github.com/akash-network/provider/cluster/kube/builder"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	"github.com/akash-network/provider/cluster/kube/operators/clients/metallb"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	clusterutil "github.com/akash-network/provider/cluster/util"
	"github.com/akash-network/provider/operator/common"
	ipoptypes "github.com/akash-network/provider/operator/ip/types"
	akashtypes "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/fromctx"
)

const (
	serviceMetalLb = "metal-lb"
)

type ipOperator struct {
	state             map[string]managedIP
	kc                kubernetes.Interface
	ac                akashclientset.Interface
	ns                string
	log               log.Logger
	server            common.OperatorHTTP
	leasesIgnored     common.IgnoreList
	flagState         common.PrepareFlagFn
	flagIgnoredLeases common.PrepareFlagFn
	flagUsage         common.PrepareFlagFn
	cfg               common.OperatorConfig
	available         uint
	inUse             uint
	mllbc             metallb.Client
	barrier           *barrier
	dataLock          sync.Locker
}

func (op *ipOperator) monitorUntilError(ctx context.Context) error {
	op.log.Info("associated provider ", "addr", op.cfg.ProviderAddress)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	op.state = make(map[string]managedIP)
	op.log.Info("fetching existing IP passthroughs")
	entries, err := op.mllbc.GetIPPassthroughs(ctx)
	if err != nil {
		return err
	}
	startupTime := time.Now()
	for _, ipPassThrough := range entries {
		k := getStateKey(ipPassThrough.GetLeaseID(), ipPassThrough.GetSharingKey(), ipPassThrough.GetExternalPort())
		op.state[k] = managedIP{
			presentLease:        ipPassThrough.GetLeaseID(),
			presentServiceName:  ipPassThrough.GetServiceName(),
			lastEvent:           nil,
			presentSharingKey:   ipPassThrough.GetSharingKey(),
			presentExternalPort: ipPassThrough.GetExternalPort(),
			presentPort:         ipPassThrough.GetPort(),
			lastChangedAt:       startupTime,
		}
	}
	op.flagState()

	// Get the present counts before starting
	err = op.updateCounts(ctx)
	if err != nil {
		return err
	}

	op.log.Info("starting observation")

	// Detect changes to the IP's declared by the provider
	events, err := op.observeIPState(ctx)
	if err != nil {
		return err
	}
	poolChanges, err := op.mllbc.DetectPoolChanges(ctx)
	if err != nil {
		return err
	}

	if err := op.server.PrepareAll(); err != nil {
		return err
	}

	var exitError error

	pruneTicker := time.NewTicker(op.cfg.PruneInterval)
	defer pruneTicker.Stop()
	prepareTicker := time.NewTicker(op.cfg.WebRefreshInterval)
	defer prepareTicker.Stop()
	updateTicker := time.NewTicker(10 * time.Minute)
	defer updateTicker.Stop()

	op.log.Info("barrier can now be passed")
	op.barrier.enable()
loop:
	for {
		isUpdating := false
		prepareData := false
		select {
		case <-ctx.Done():
			exitError = ctx.Err()
			break loop

		case ev, ok := <-events:
			if !ok {
				exitError = common.ErrObservationStopped
				break loop
			}
			err = op.applyEvent(ctx, ev)
			if err != nil {
				op.log.Error("failed applying event", "err", err)
				exitError = err
				break loop
			}

			isUpdating = true
		case <-pruneTicker.C:
			op.leasesIgnored.Prune()
			op.flagIgnoredLeases()
			prepareData = true
		case <-prepareTicker.C:
			prepareData = true
		case <-updateTicker.C:
			isUpdating = true
		case _, ok := <-poolChanges:
			if !ok {
				break loop
			}
			isUpdating = true
		}

		if isUpdating {
			err = op.updateCounts(ctx)
			if err != nil {
				exitError = err
				break loop
			}
			isUpdating = false
			prepareData = true
		}

		if prepareData {
			if err := op.server.PrepareAll(); err != nil {
				op.log.Error("preparing web data failed", "err", err)
			}
		}
	}
	op.barrier.disable()

	// Wait for up to 30 seconds
	ctxWithTimeout, timeoutCtxCancel := context.WithTimeout(context.Background(), time.Second*30)
	defer timeoutCtxCancel()

	err = op.barrier.waitUntilClear(ctxWithTimeout)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			op.log.Error("did not clear barrier in given time")
		} else {
			op.log.Error("failed waiting on barrier to clear", "err", err)
		}
	}

	op.log.Info("ip operator done")

	return exitError
}

func (op *ipOperator) updateCounts(parentCtx context.Context) error {
	// This is tried in a loop, don't wait for a long period of time for a response
	ctx, cancel := context.WithTimeout(parentCtx, time.Minute)
	defer cancel()
	inUse, available, err := op.mllbc.GetIPAddressUsage(ctx)
	if err != nil {
		return err
	}

	op.dataLock.Lock()
	defer op.dataLock.Unlock()
	op.inUse = inUse
	op.available = available

	op.flagUsage()
	op.log.Info("ip address inventory", "in-use", op.inUse, "available", op.available)
	return nil
}

func (op *ipOperator) recordEventError(ev cip.ResourceEvent, failure error) {
	// ff no error, no action
	if failure == nil {
		return
	}

	mark := kubeErrors.IsNotFound(failure)

	if !mark {
		return
	}

	op.log.Info("recording error for", "lease", ev.GetLeaseID().String(), "err", failure)
	op.leasesIgnored.AddError(ev.GetLeaseID(), failure, ev.GetSharingKey())
	op.flagIgnoredLeases()
}

func (op *ipOperator) applyEvent(ctx context.Context, ev cip.ResourceEvent) error {
	op.log.Debug("apply event", "event-type", ev.GetEventType(), "lease", ev.GetLeaseID())
	switch ev.GetEventType() {
	case ctypes.ProviderResourceDelete:
		// note that on delete the resource might be gone anyway because the namespace is deleted
		return op.applyDeleteEvent(ctx, ev)
	case ctypes.ProviderResourceAdd, ctypes.ProviderResourceUpdate:
		if op.leasesIgnored.IsFlagged(ev.GetLeaseID()) {
			op.log.Info("ignoring event for", "lease", ev.GetLeaseID().String())
			return nil
		}
		err := op.applyAddOrUpdateEvent(ctx, ev)
		op.recordEventError(ev, err)
		return err
	default:
		return fmt.Errorf("%w: unknown event type %v", common.ErrObservationStopped, ev.GetEventType())
	}
}

func (op *ipOperator) applyDeleteEvent(parentCtx context.Context, ev cip.ResourceEvent) error {
	directive := buildIPDirective(ev)

	// Delete events are a one-shot type thing. The operator always queries for existing CRDs but can't
	// query for the non-existence of something. The timeout used here is considerably higher as a result
	// In the future the operator can be improved by adding an optional purge routing which seeks out kube resources
	// for services that allocate an IP but that do not belong to at least 1 CRD
	ctx, cancel := context.WithTimeout(parentCtx, time.Minute*5)
	defer cancel()
	err := op.mllbc.PurgeIPPassthrough(ctx, directive)

	if err == nil {
		uid := getStateKey(ev.GetLeaseID(), ev.GetSharingKey(), ev.GetExternalPort())
		delete(op.state, uid)
		op.flagState()
	}

	return err
}

func buildIPDirective(ev cip.ResourceEvent) cip.ClusterIPPassthroughDirective {
	return cip.ClusterIPPassthroughDirective{
		LeaseID:      ev.GetLeaseID(),
		ServiceName:  ev.GetServiceName(),
		Port:         ev.GetPort(),
		ExternalPort: ev.GetExternalPort(),
		SharingKey:   ev.GetSharingKey(),
		Protocol:     ev.GetProtocol(),
	}
}

func getStateKey(leaseID mtypes.LeaseID, sharingKey string, externalPort uint32) string {
	// TODO - need to double check this makes sense
	return fmt.Sprintf("%v-%s-%d", leaseID.GetOwner(), sharingKey, externalPort)
}

func (op *ipOperator) applyAddOrUpdateEvent(ctx context.Context, ev cip.ResourceEvent) error {
	leaseID := ev.GetLeaseID()

	uid := getStateKey(ev.GetLeaseID(), ev.GetSharingKey(), ev.GetExternalPort())

	op.log.Info("connecting",
		"lease", leaseID,
		"service", ev.GetServiceName(),
		"externalPort", ev.GetExternalPort())
	entry, exists := op.state[uid]

	isSameLease := true
	if exists {
		isSameLease = entry.presentLease.Equals(leaseID)
	}

	directive := buildIPDirective(ev)

	var err error
	shouldConnect := false

	if isSameLease {
		if !exists {
			shouldConnect = true
			op.log.Debug("ip passthrough is new, applying", "lease", leaseID)
			// Check to see if port or service name is different
		} else {
			hasChanged := entry.presentServiceName != ev.GetServiceName() ||
				entry.presentPort != ev.GetPort() ||
				entry.presentSharingKey != ev.GetSharingKey() ||
				entry.presentExternalPort != ev.GetExternalPort() ||
				entry.presentProtocol != ev.GetProtocol()
			if hasChanged {
				shouldConnect = true
				op.log.Debug("ip passthrough has changed, applying", "lease", leaseID)
			}
		}

		if shouldConnect {
			op.log.Debug("Updating ip passthrough", "lease", leaseID)
			err = op.mllbc.CreateIPPassthrough(ctx, directive)
		}
	} else {

		/** TODO - the sharing key keeps the IP the same unless
		this directive purges all the services using that key. This creates
		a problem where the IP could change. This is not the desired behavior in the system
		We may need to add a bogus service here temporarily to prevent that from happening
		*/
		deleteDirective := cip.ClusterIPPassthroughDirective{
			LeaseID:      entry.presentLease,
			ServiceName:  entry.presentServiceName,
			Port:         entry.presentPort,
			ExternalPort: entry.presentExternalPort,
			SharingKey:   entry.presentSharingKey,
			Protocol:     entry.presentProtocol,
		}
		// Delete the entry & recreate it with the new lease associated  to it
		err = op.mllbc.PurgeIPPassthrough(ctx, deleteDirective)
		if err != nil {
			return err
		}
		// Remove the current value from the state
		delete(op.state, uid)
		err = op.mllbc.CreateIPPassthrough(ctx, directive)
	}

	if err != nil {
		return err
	}

	// Update stored entry
	entry.presentServiceName = ev.GetServiceName()
	entry.presentLease = leaseID
	entry.lastEvent = ev
	entry.presentExternalPort = ev.GetExternalPort()
	entry.presentSharingKey = ev.GetSharingKey()
	entry.presentPort = ev.GetPort()
	entry.presentProtocol = ev.GetProtocol()
	entry.lastChangedAt = time.Now()
	op.state[uid] = entry
	op.flagState()

	op.log.Info("update complete", "lease", leaseID)

	return nil
}

func (op *ipOperator) prepareUsage(pd common.PreparedResult) error {
	op.dataLock.Lock()
	defer op.dataLock.Unlock()
	value := cip.AddressUsage{
		Available: op.available,
		InUse:     op.inUse,
	}

	buf := &bytes.Buffer{}
	encoder := json.NewEncoder(buf)

	err := encoder.Encode(value)
	if err != nil {
		return err
	}

	pd.Set(buf.Bytes())
	return nil
}

func (op *ipOperator) prepareState(pd common.PreparedResult) error {
	results := make(map[string][]interface{})
	for _, managedIPEntry := range op.state {
		leaseID := managedIPEntry.presentLease

		result := struct {
			LastChangeTime string         `json:"last-event-time,omitempty"`
			LeaseID        mtypes.LeaseID `json:"lease-id"`
			Namespace      string         `json:"namespace"` // diagnostic only
			Port           uint32         `json:"port"`
			ExternalPort   uint32         `json:"external-port"`
			ServiceName    string         `json:"service-name"`
			SharingKey     string         `json:"sharing-key"`
		}{
			LeaseID:        leaseID,
			Namespace:      clusterutil.LeaseIDToNamespace(leaseID),
			Port:           managedIPEntry.presentPort,
			ExternalPort:   managedIPEntry.presentExternalPort,
			ServiceName:    managedIPEntry.presentServiceName,
			SharingKey:     managedIPEntry.presentSharingKey,
			LastChangeTime: managedIPEntry.lastChangedAt.UTC().String(),
		}

		entryList := results[leaseID.String()]
		entryList = append(entryList, result)
		results[leaseID.String()] = entryList
	}

	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	err := enc.Encode(results)
	if err != nil {
		return err
	}

	pd.Set(buf.Bytes())
	return nil
}

func handleHTTPError(op *ipOperator, rw http.ResponseWriter, req *http.Request, err error, status int) {
	op.log.Error("http request processing failed", "method", req.Method, "path", req.URL.Path, "err", err)
	rw.WriteHeader(status)

	body := ipoptypes.IPOperatorErrorResponse{
		Error: err.Error(),
		Code:  -1,
	}

	if errors.Is(err, ipoptypes.ErrIPOperator) {
		code := err.(ipoptypes.IPOperatorError).GetCode()
		body.Code = code
	}

	encoder := json.NewEncoder(rw)
	err = encoder.Encode(body)
	if err != nil {
		op.log.Error("failed writing response body", "err", err)
	}
}

func newIPOperator(ctx context.Context, logger log.Logger, ns string, cfg common.OperatorConfig, ilc common.IgnoreListConfig, mllbc metallb.Client) (*ipOperator, error) {
	kc, err := fromctx.KubeClientFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	ac, err := fromctx.AkashClientFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	opHTTP, err := common.NewOperatorHTTP()
	if err != nil {
		return nil, err
	}
	retval := &ipOperator{
		state:         make(map[string]managedIP),
		kc:            kc,
		ac:            ac,
		ns:            ns,
		log:           logger,
		server:        opHTTP,
		leasesIgnored: common.NewIgnoreList(ilc),
		mllbc:         mllbc,
		dataLock:      &sync.Mutex{},
		barrier:       &barrier{},
		cfg:           cfg,
	}

	retval.server.GetRouter().Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if !retval.barrier.enter() {
				retval.log.Error("barrier is locked, can't service request", "path", req.URL.Path)
				rw.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(rw, req)
			retval.barrier.exit()
		})
	})

	retval.flagState = retval.server.AddPreparedEndpoint("/state", retval.prepareState)
	retval.flagIgnoredLeases = retval.server.AddPreparedEndpoint("/ignored-leases", retval.leasesIgnored.Prepare)
	retval.flagUsage = retval.server.AddPreparedEndpoint("/usage", retval.prepareUsage)

	retval.server.GetRouter().HandleFunc("/health", func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(rw, "OK")
	})

	// TODO - add auth based off TokenReview via k8s interface to below methods OR just cache these so they can't abuse kube
	retval.server.GetRouter().HandleFunc("/ip-lease-status/{owner}/{dseq}/{gseq}/{oseq}", func(rw http.ResponseWriter, req *http.Request) {
		handleIPLeaseStatusGet(retval, rw, req)
	}).Methods(http.MethodGet)
	return retval, nil
}

func handleIPLeaseStatusGet(op *ipOperator, rw http.ResponseWriter, req *http.Request) {
	// Extract path variables, returning 404 if any are invalid
	vars := mux.Vars(req)
	dseqStr := vars["dseq"]
	dseq, err := strconv.ParseUint(dseqStr, 10, 64)
	if err != nil {
		op.log.Error("could not parse path component as uint64", "dseq", dseqStr, "error", err)
		rw.WriteHeader(http.StatusNotFound)
		return
	}

	gseqStr := vars["gseq"]
	gseq, err := strconv.ParseUint(gseqStr, 10, 32)
	if err != nil {
		op.log.Error("could not parse path component as uint32", "gseq", gseqStr, "error", err)
		rw.WriteHeader(http.StatusNotFound)
		return
	}

	oseqStr := vars["oseq"]
	oseq, err := strconv.ParseUint(oseqStr, 10, 32)
	if err != nil {
		op.log.Error("could not parse path component as uint32", "oseq", oseqStr, "error", err)
		rw.WriteHeader(http.StatusNotFound)
		return
	}

	owner := vars["owner"]
	_, err = sdk.AccAddressFromBech32(owner) // Validate this is a bech32 address
	if err != nil {
		op.log.Error("could not parse owner address as bech32", "onwer", owner, "error", err)
		rw.WriteHeader(http.StatusNotFound)
		return
	}

	leaseID := mtypes.LeaseID{
		Owner:    owner,
		DSeq:     dseq,
		GSeq:     uint32(gseq),
		OSeq:     uint32(oseq),
		Provider: op.cfg.ProviderAddress,
	}

	ipStatus, err := op.mllbc.GetIPAddressStatusForLease(req.Context(), leaseID)
	if err != nil {
		op.log.Error("Could not get IP address status", "lease-id", leaseID, "error", err)
		handleHTTPError(op, rw, req, err, http.StatusInternalServerError)
		return
	}

	if len(ipStatus) == 0 {
		op.log.Debug("no IP address to status for lease", "lease-id", leaseID)
		rw.WriteHeader(http.StatusNoContent)
		return
	}

	op.log.Debug("retrieved IP address status for lease", "lease-id", leaseID, "quantity", len(ipStatus))

	rw.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(rw)
	// ipStatus is a slice of interface types, so it can't be encoded directly
	responseData := make([]cip.LeaseIPStatus, len(ipStatus))
	for i, v := range ipStatus {
		responseData[i] = cip.LeaseIPStatus{
			Port:         v.GetPort(),
			ExternalPort: v.GetExternalPort(),
			ServiceName:  v.GetServiceName(),
			IP:           v.GetIP(),
			Protocol:     v.GetProtocol().ToString(),
		}
	}
	err = encoder.Encode(responseData)
	if err != nil {
		op.log.Error("failed writing JSON of ip status response", "error", err)
	}
}

func (op *ipOperator) run(parentCtx context.Context) error {
	op.log.Info("ip operator start")
	for {
		lastAttempt := time.Now()
		err := op.monitorUntilError(parentCtx)
		if errors.Is(err, context.Canceled) {
			op.log.Debug("ip operator terminate")
			break
		}

		op.log.Error("observation stopped", "err", err)

		// don't spin if there is a condition causing fast failure
		elapsed := time.Since(lastAttempt)
		if elapsed < op.cfg.RetryDelay {
			op.log.Info("delaying")
			select {
			case <-parentCtx.Done():
				return parentCtx.Err()
			case <-time.After(op.cfg.RetryDelay):
				// delay complete
			}
		}
	}

	op.mllbc.Stop()
	return parentCtx.Err()
}

const (
	serviceNameLabel  = "service-name"
	externalPortLabel = "external-port"
	protoLabel        = "proto"
)

// nolint: unused
func (op *ipOperator) getDeclaredIPs(ctx context.Context, leaseID mtypes.LeaseID) ([]akashtypes.ProviderLeasedIPSpec, error) {
	labelSelector := &strings.Builder{}
	kubeSelectorForLease(labelSelector, leaseID)

	results, err := op.ac.AkashV2beta2().ProviderLeasedIPs(op.ns).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})

	if err != nil {
		return nil, err
	}

	retval := make([]akashtypes.ProviderLeasedIPSpec, 0, len(results.Items))
	for _, item := range results.Items {
		retval = append(retval, item.Spec)
	}

	return retval, nil
}

func (op *ipOperator) PurgeDeclaredIP(ctx context.Context, leaseID mtypes.LeaseID, serviceName string, externalPort uint32, proto manifest.ServiceProtocol) error {
	labelSelector := &strings.Builder{}
	kubeSelectorForLease(labelSelector, leaseID)
	_, _ = fmt.Fprintf(labelSelector, ",%s=%s", serviceNameLabel, serviceName)
	_, _ = fmt.Fprintf(labelSelector, ",%s=%s", protoLabel, proto.ToString())
	_, _ = fmt.Fprintf(labelSelector, ",%s=%d", externalPortLabel, externalPort)
	return op.ac.AkashV2beta2().ProviderLeasedIPs(op.ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", builder.AkashManagedLabelName),
	})
}

func (op *ipOperator) DeclareIP(ctx context.Context, lID mtypes.LeaseID, serviceName string, port uint32, externalPort uint32, proto manifest.ServiceProtocol, sharingKey string, overwrite bool) error {
	// Note: This interface expects sharing key to contain a value that is unique per deployment owner, in this
	// case it is the bech32 address, or a derivative thereof
	resourceName := strings.ToLower(fmt.Sprintf("%s-%s-%d", sharingKey, proto.ToString(), externalPort))

	op.log.Debug("checking for resource", "resource-name", resourceName)
	foundEntry, err := op.ac.AkashV2beta2().ProviderLeasedIPs(op.ns).Get(ctx, resourceName, metav1.GetOptions{})
	exists := true
	if err != nil {
		if !kubeErrors.IsNotFound(err) {
			return err
		}
		exists = false
	}

	if exists && !overwrite {
		return kubeclienterrors.ErrAlreadyExists
	}

	labels := map[string]string{
		builder.AkashManagedLabelName: "true",
		serviceNameLabel:              serviceName,
		externalPortLabel:             fmt.Sprintf("%d", externalPort),
		protoLabel:                    proto.ToString(),
	}
	builder.AppendLeaseLabels(lID, labels)

	obj := akashtypes.ProviderLeasedIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:   resourceName,
			Labels: labels,
		},
		Spec: akashtypes.ProviderLeasedIPSpec{
			LeaseID:      akashtypes.LeaseIDFromAkash(lID),
			ServiceName:  serviceName,
			ExternalPort: externalPort,
			SharingKey:   sharingKey,
			Protocol:     proto.ToString(),
			Port:         port,
		},
	}

	op.log.Info("declaring leased ip", "lease", lID,
		"service-name", serviceName,
		"port", port,
		"external-port", externalPort,
		"sharing-key", sharingKey,
		"exists", exists)
	// Create or update the entry
	if exists {
		obj.ObjectMeta.ResourceVersion = foundEntry.ResourceVersion
		_, err = op.ac.AkashV2beta2().ProviderLeasedIPs(op.ns).Update(ctx, &obj, metav1.UpdateOptions{})
	} else {
		_, err = op.ac.AkashV2beta2().ProviderLeasedIPs(op.ns).Create(ctx, &obj, metav1.CreateOptions{})
	}

	return err
}

// nolint: unused
func (op *ipOperator) purgeDeclaredIPs(ctx context.Context, lID mtypes.LeaseID) error {
	labelSelector := &strings.Builder{}
	_, err := fmt.Fprintf(labelSelector, "%s=true,", builder.AkashManagedLabelName)
	if err != nil {
		return err
	}
	kubeSelectorForLease(labelSelector, lID)
	result := op.ac.AkashV2beta2().ProviderLeasedIPs(op.ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})

	return result
}

func (op *ipOperator) DetectPoolChanges(ctx context.Context) (<-chan struct{}, error) {
	const metalLBNamespace = "metallb-system"

	watcher, err := op.kc.CoreV1().ConfigMaps(metalLBNamespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	output := make(chan struct{}, 1)
	go func() {
		defer close(output)
		for {
			select {
			case <-ctx.Done():
				err = ctx.Err()
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					op.log.Error("failed watching metal LB config map changes", "err", err)
				}
				return
			case ev, ok := <-watcher.ResultChan():
				if !ok { // Channel closed when an error happens
					return
				}
				// Do not log the whole event, it is too verbose
				op.log.Debug("metal LB config change event", "event-type", ev.Type)
				output <- struct{}{}
			}
		}
	}()

	return output, nil
}

func (op *ipOperator) observeIPState(ctx context.Context) (<-chan cip.ResourceEvent, error) {
	var lastResourceVersion string
	phpager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		resources, err := op.ac.AkashV2beta2().ProviderLeasedIPs(op.ns).List(ctx, opts)

		if err == nil && len(resources.GetResourceVersion()) != 0 {
			lastResourceVersion = resources.GetResourceVersion()
		}
		return resources, err
	})

	data := make([]akashtypes.ProviderLeasedIP, 0, 128)
	err := phpager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		plip := obj.(*akashtypes.ProviderLeasedIP)
		data = append(data, *plip)
		return nil
	})

	if err != nil {
		return nil, err
	}

	op.log.Info("starting ip passthrough watch", "resourceVersion", lastResourceVersion)
	watcher, err := op.ac.AkashV2beta2().ProviderLeasedIPs(op.ns).Watch(ctx, metav1.ListOptions{
		TypeMeta:             metav1.TypeMeta{},
		LabelSelector:        "",
		FieldSelector:        "",
		Watch:                false,
		AllowWatchBookmarks:  false,
		ResourceVersion:      lastResourceVersion,
		ResourceVersionMatch: "",
		TimeoutSeconds:       nil,
		Limit:                0,
		Continue:             "",
	})
	if err != nil {
		return nil, err
	}

	evData := make([]ipResourceEvent, len(data))
	for i, v := range data {
		ownerAddr, err := sdktypes.AccAddressFromBech32(v.Spec.LeaseID.Owner)
		if err != nil {
			return nil, err
		}
		providerAddr, err := sdktypes.AccAddressFromBech32(v.Spec.LeaseID.Provider)
		if err != nil {
			return nil, err
		}

		leaseID, err := v.Spec.LeaseID.FromCRD()
		if err != nil {
			return nil, err
		}

		proto, err := manifest.ParseServiceProtocol(v.Spec.Protocol)
		if err != nil {
			return nil, err
		}

		ev := ipResourceEvent{
			eventType:    ctypes.ProviderResourceAdd,
			lID:          leaseID,
			serviceName:  v.Spec.ServiceName,
			port:         v.Spec.Port,
			externalPort: v.Spec.ExternalPort,
			ownerAddr:    ownerAddr,
			providerAddr: providerAddr,
			sharingKey:   v.Spec.SharingKey,
			protocol:     proto,
		}
		evData[i] = ev
	}

	data = nil

	output := make(chan cip.ResourceEvent)

	go func() {
		defer close(output)
		for _, v := range evData {
			output <- v
		}
		evData = nil // do not hold the reference

		results := watcher.ResultChan()
		for {
			select {
			case result, ok := <-results:
				if !ok { // Channel closed when an error happens
					return
				}
				plip := result.Object.(*akashtypes.ProviderLeasedIP)
				ownerAddr, err := sdktypes.AccAddressFromBech32(plip.Spec.LeaseID.Owner)
				if err != nil {
					op.log.Error("invalid owner address in provider host", "addr", plip.Spec.LeaseID.Owner, "err", err)
					continue // Ignore event
				}
				providerAddr, err := sdktypes.AccAddressFromBech32(plip.Spec.LeaseID.Provider)
				if err != nil {
					op.log.Error("invalid provider address in provider host", "addr", plip.Spec.LeaseID.Provider, "err", err)
					continue // Ignore event
				}
				leaseID, err := plip.Spec.LeaseID.FromCRD()
				if err != nil {
					op.log.Error("invalid lease ID", "err", err)
					continue // Ignore event
				}
				proto, err := manifest.ParseServiceProtocol(plip.Spec.Protocol)
				if err != nil {
					op.log.Error("invalid protocol", "err", err)
					continue
				}

				ev := ipResourceEvent{
					lID:          leaseID,
					serviceName:  plip.Spec.ServiceName,
					port:         plip.Spec.Port,
					externalPort: plip.Spec.ExternalPort,
					sharingKey:   plip.Spec.SharingKey,
					providerAddr: providerAddr,
					ownerAddr:    ownerAddr,
					protocol:     proto,
				}
				switch result.Type {

				case watch.Added:
					ev.eventType = ctypes.ProviderResourceAdd
				case watch.Modified:
					ev.eventType = ctypes.ProviderResourceUpdate
				case watch.Deleted:
					ev.eventType = ctypes.ProviderResourceDelete

				case watch.Error:
					// Based on examination of the implementation code, this is basically never called anyways
					op.log.Error("watch error", "err", result.Object)

				default:
					continue
				}

				output <- ev
			case <-ctx.Done():
				return
			}
		}
	}()

	return output, nil
}

func kubeSelectorForLease(dst *strings.Builder, lID mtypes.LeaseID) {
	_, _ = fmt.Fprintf(dst, "%s=%s", builder.AkashLeaseOwnerLabelName, lID.Owner)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseDSeqLabelName, lID.DSeq)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseGSeqLabelName, lID.GSeq)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseOSeqLabelName, lID.OSeq)
}

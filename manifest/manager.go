package manifest

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/boz/go-lifecycle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"cosmossdk.io/log"
	grpctypes "github.com/cosmos/cosmos-sdk/types/grpc"

	maniv2beta2 "pkg.akt.dev/go/manifest/v2beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	"pkg.akt.dev/go/util/pubsub"
	"pkg.akt.dev/go/util/runner"

	clustertypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
)

const (
	manifestLingerDuration = time.Minute * time.Duration(5)
)

var (
	// ErrShutdownTimerExpired for a terminating deployment
	ErrShutdownTimerExpired = errors.New("shutdown timer expired")
	// ErrManifestVersion indicates that the given manifest's version does not
	// match the blockchain Version value.
	ErrManifestVersion         = errors.New("manifest version validation failed")
	ErrNoManifestForDeployment = errors.New("manifest not yet received for that deployment")
	ErrNoLeaseForDeployment    = errors.New("no lease for deployment")
	errNoGroupForLease         = errors.New("group not found")
	errManifestRejected        = errors.New("manifest rejected")
)

func newManager(h *service, daddr dtypes.DeploymentID) *manager {
	session := h.session.ForModule("manifest-manager")

	m := &manager{
		daddr:           daddr,
		session:         session,
		bus:             h.bus,
		leasech:         make(chan event.LeaseWon),
		rmleasech:       make(chan mtypes.LeaseID),
		manifestch:      make(chan manifestRequest),
		updatech:        make(chan []byte),
		log:             session.Log().With("did", daddr),
		lc:              lifecycle.New(),
		config:          h.config,
		hostnameService: h.hostnameService,
	}

	go m.lc.WatchChannel(h.lc.ShuttingDown())
	go m.run(h.managerch)

	return m
}

// 'manager' facilitates operations around a configured Deployment.
type manager struct {
	config  ServiceConfig
	daddr   dtypes.DeploymentID
	session session.Session
	bus     pubsub.Bus

	leasech    chan event.LeaseWon
	rmleasech  chan mtypes.LeaseID
	manifestch chan manifestRequest
	updatech   chan []byte

	data            dvbeta.QueryDeploymentResponse
	requests        []manifestRequest
	pendingRequests []manifestRequest
	manifests       []*maniv2beta2.Manifest
	// expected is the deployment hash from the chain read at the greatest block height so
	// far; a manifest must match it. clearFetched leaves it alone, so a read from a node
	// that lags behind an earlier one cannot move it back.
	expected deploymentVersion

	localLeases []event.LeaseWon
	fetched     bool
	fetchedAt   time.Time

	stoptimer *time.Timer

	log log.Logger
	lc  lifecycle.Lifecycle

	hostnameService clustertypes.HostnameServiceClient
}

func (m *manager) stop() {
	m.lc.ShutdownAsync(nil)
}

func (m *manager) handleLease(ev event.LeaseWon) {
	select {
	case m.leasech <- ev:
	case <-m.lc.ShuttingDown():
		m.log.Error("not running: handle manifest", "lid", ev.LeaseID)
	}
}

func (m *manager) removeLease(id mtypes.LeaseID) {
	select {
	case m.rmleasech <- id:
	case <-m.lc.ShuttingDown():
		m.log.Error("not running: remove lease", "lid", id)
	}
}

func (m *manager) handleManifest(req manifestRequest) {
	select {
	case m.manifestch <- req:
	case <-m.lc.ShuttingDown():
		m.log.Error("not running: handle manifest")
		req.ch <- ErrNotRunning
	}
}

func (m *manager) handleUpdate(version []byte) {
	select {
	case m.updatech <- version:
	case <-m.lc.ShuttingDown():
		m.log.Error("not running: version update", "version", hex.EncodeToString(version))
	}
}

func (m *manager) clearFetched() {
	m.fetchedAt = time.Time{}
	m.fetched = false
	m.data = dvbeta.QueryDeploymentResponse{}
	m.localLeases = nil
}

func (m *manager) run(donech chan<- *manager) {
	defer m.lc.ShutdownCompleted()
	defer func() { donech <- m }()

	var runch <-chan runner.Result

	ctx, cancel := context.WithCancel(context.Background())

loop:
	for {
		var stopch <-chan time.Time
		if m.stoptimer != nil {
			stopch = m.stoptimer.C
		}

		select {
		case err := <-m.lc.ShutdownRequest():
			m.lc.ShutdownInitiated(err)
			break loop

		case <-stopch:
			m.log.Error(ErrShutdownTimerExpired.Error())
			m.lc.ShutdownInitiated(ErrShutdownTimerExpired)
			break loop

		case ev := <-m.leasech:
			m.log.Info("new lease", "lid", ev.LeaseID)

			// fetch owner's public key
			m.clearFetched()
			m.maybeScheduleStop()

			runch = m.maybeFetchData(ctx, runch)
		case id := <-m.rmleasech:
			m.log.Info("lease removed", "lid", id)
			m.clearFetched()
			m.maybeScheduleStop()
		case req := <-m.manifestch:
			m.log.Info("manifest received")

			m.requests = append(m.requests, req)
			m.maybeScheduleStop()

			if runch = m.maybeFetchData(ctx, runch); runch == nil {
				m.validateRequests()
				m.maybeEmitReceivedEvents()
				runch = m.maybeRereadData(ctx)
			}
		case version := <-m.updatech:
			m.applyUpdate(version)

		case result := <-runch:
			runch = nil

			if err := result.Error(); err != nil {
				m.log.Error("error fetching data", "err", err)
				// Fetching data failed, all requests are now in an error state
				m.fillAllRequests(err)
				break
			}

			fetchResult := result.Value().(manifestManagerFetchDataResult)
			m.applyFetchResult(fetchResult)

			m.log.Info("data received", "version", hex.EncodeToString(m.data.Deployment.Hash), "height", fetchResult.height)

			m.validateRequests()
			m.maybeEmitReceivedEvents()
			m.maybeScheduleStop()
			runch = m.maybeRereadData(ctx)
		}
	}

	cancel()

	m.fillAllRequests(ErrNotRunning)

	if m.stoptimer != nil {
		if m.stoptimer.Stop() {
			<-m.stoptimer.C
		}
	}

	if runch != nil {
		<-runch
	}
}

// maybeFetchData try fetch deployment and lease data
// if there is cached result the function returns nil channel which signals caller to process events
func (m *manager) maybeFetchData(ctx context.Context, runch <-chan runner.Result) <-chan runner.Result {
	if runch != nil {
		return runch
	}

	if !m.fetched || time.Since(m.fetchedAt) > m.config.CachedResultMaxAge {
		m.clearFetched()
		return m.fetchData(ctx)
	}
	return runch
}

// maybeRereadData starts a new chain read if validateRequests kept requests waiting for one,
// and returns nil otherwise. Callers only use it while no read is running.
func (m *manager) maybeRereadData(ctx context.Context) <-chan runner.Result {
	if len(m.requests) == 0 {
		return nil
	}

	m.clearFetched()

	return m.fetchData(ctx)
}

func (m *manager) fetchData(ctx context.Context) <-chan runner.Result {
	return runner.Do(func() runner.Result {
		// TODO: retry
		return runner.NewResult(m.doFetchData(ctx))
	})
}

type manifestManagerFetchDataResult struct {
	deployment dvbeta.QueryDeploymentResponse
	leases     []event.LeaseWon
	// height is the block height the deployment was read at, 0 if the node did not say.
	height int64
}

// deploymentVersion is a deployment hash and the block height a chain read returned it at.
type deploymentVersion struct {
	hash   []byte
	height int64
}

// applyUpdate handles a DeploymentUpdated event. It only drops the cached chain read, so
// the next manifest is checked against a fresh one: an event can be missed or arrive late,
// so the expected version comes from chain reads alone.
func (m *manager) applyUpdate(version []byte) {
	m.log.Info("received version", "version", hex.EncodeToString(version))
	m.clearFetched()
}

// applyFetchResult stores a chain read. Its deployment hash becomes the expected version
// unless an earlier read was answered at a greater height. A read without a height (0)
// counts only while no read has reported one.
func (m *manager) applyFetchResult(result manifestManagerFetchDataResult) {
	m.fetched = true
	m.fetchedAt = time.Now()
	m.data = result.deployment
	m.localLeases = result.leases

	hash := result.deployment.Deployment.Hash
	if len(hash) == 0 {
		return
	}

	if result.height < m.expected.height {
		m.log.Info("chain read behind an earlier one, keeping expected version",
			"version", hex.EncodeToString(hash), "height", result.height,
			"expected", hex.EncodeToString(m.expected.hash), "expected_height", m.expected.height)
		return
	}

	m.expected = deploymentVersion{hash: hash, height: result.height}
}

// readHeight returns the block height a chain query was answered at, from the response
// header Cosmos SDK sets, or 0 if the node did not report one.
func readHeight(header metadata.MD) int64 {
	values := header.Get(grpctypes.GRPCBlockHeightHeader)
	if len(values) == 0 {
		return 0
	}

	height, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || height < 0 {
		return 0
	}

	return height
}

func (m *manager) doFetchData(ctx context.Context) (manifestManagerFetchDataResult, error) {
	subctx, cancel := context.WithTimeout(ctx, m.config.RPCQueryTimeout)
	defer cancel()

	var header metadata.MD
	deploymentResponse, err := m.session.Client().Query().Deployment().Deployment(subctx,
		&dvbeta.QueryDeploymentRequest{ID: m.daddr}, grpc.Header(&header))
	if err != nil {
		return manifestManagerFetchDataResult{}, err
	}
	leasesResponse, err := m.session.Client().Query().Market().Leases(subctx, &mvbeta.QueryLeasesRequest{
		Filters: mtypes.LeaseFilters{
			Owner:    m.daddr.Owner,
			DSeq:     m.daddr.DSeq,
			GSeq:     0,
			OSeq:     0,
			Provider: m.session.Provider().GetOwner(),
			State:    mtypes.LeaseActive.String(),
		},
		Pagination: nil,
	})
	if err != nil {
		return manifestManagerFetchDataResult{}, err
	}

	groups := make(map[uint32]dvbeta.Group)
	for _, g := range deploymentResponse.GetGroups() {
		groups[g.ID.GSeq] = g
	}

	leases := make([]event.LeaseWon, len(leasesResponse.Leases))
	for i, leaseEntry := range leasesResponse.Leases {
		lease := leaseEntry.GetLease()
		leaseID := lease.ID
		groupForLease, foundGroup := groups[leaseID.GetGSeq()]
		if !foundGroup {
			return manifestManagerFetchDataResult{}, fmt.Errorf("%w: could not locate group %v ", errNoGroupForLease, leaseID)
		}
		ev := event.LeaseWon{
			LeaseID: leaseID,
			Group:   &groupForLease,
			Price:   lease.GetPrice(),
		}

		leases[i] = ev
	}

	return manifestManagerFetchDataResult{
		deployment: *deploymentResponse,
		leases:     leases,
		height:     readHeight(header),
	}, nil
}

func (m *manager) maybeScheduleStop() bool { // nolint:unparam
	if len(m.localLeases) > 0 || len(m.manifests) > 0 {
		if m.stoptimer != nil {
			m.log.Info("stopping stop timer")
			if m.stoptimer.Stop() {
				<-m.stoptimer.C
			}
			m.stoptimer = nil
		}
		return false
	}
	if m.stoptimer != nil {
		m.log.Info("starting stop timer", "duration", manifestLingerDuration)
		m.stoptimer = time.NewTimer(manifestLingerDuration)
	}
	return true
}

func (m *manager) fillAllRequests(response error) {
	for _, req := range m.pendingRequests {
		req.ch <- response
	}
	m.pendingRequests = nil

	for _, req := range m.requests {
		req.ch <- response
	}
	m.requests = nil
}

// maybeEmitReceivedEvents publishes the latest manifest unless requests wait for another
// chain read and none was accepted. The re-read publishes then, so the cluster does not get
// the previous manifest again just before a new one.
func (m *manager) maybeEmitReceivedEvents() {
	if len(m.requests) > 0 && len(m.pendingRequests) == 0 {
		return
	}

	m.emitReceivedEvents()
}

func (m *manager) emitReceivedEvents() {
	if !m.fetched || len(m.manifests) == 0 {
		m.log.Debug("emit received events skipped", "manifests", len(m.manifests))
		return
	}

	if len(m.localLeases) == 0 {
		m.log.Debug("emit received events skips due to no leases", "manifests", len(m.manifests))
		m.fillAllRequests(ErrNoLeaseForDeployment)
		return
	}

	latestManifest := m.manifests[len(m.manifests)-1]
	m.log.Debug("publishing manifest received", "num_leases", len(m.localLeases))
	copyOfData := new(dvbeta.QueryDeploymentResponse)
	*copyOfData = m.data
	for _, lease := range m.localLeases {
		m.log.Debug("publishing manifest received for lease", "lid", lease.LeaseID)
		if err := m.bus.Publish(event.ManifestReceived{
			LeaseID:    lease.LeaseID,
			Group:      lease.Group,
			Manifest:   latestManifest,
			Deployment: copyOfData,
		}); err != nil {
			m.log.Error("publishing event", "err", err, "lid", lease.LeaseID)
		}
	}

	// A manifest has been published, satisfy all pending requests
	for _, req := range m.pendingRequests {
		req.ch <- nil
	}
	m.pendingRequests = nil
}

func (m *manager) validateRequests() {
	if !m.fetched || len(m.requests) == 0 {
		return
	}

	var reread []manifestRequest

	manifests := make([]*maniv2beta2.Manifest, 0)
	for _, req := range m.requests {
		// If the request context is complete then skip processing it
		select {
		case <-req.ctx.Done():
			continue
		default:
		}
		if err := m.validateRequest(req); err != nil {
			if errors.Is(err, ErrManifestVersion) && !req.reread {
				// The chain read may predate the update this manifest is for:
				// read the chain once more before refusing it.
				m.log.Info("manifest version mismatch, reading the chain again")
				req.reread = true
				reread = append(reread, req)
				continue
			}
			m.log.Error("invalid manifest", "err", err)
			req.ch <- err
			continue
		}
		manifests = append(manifests, &req.value.Manifest)

		// The manifest has been grabbed from the request but not published yet, store this response
		m.pendingRequests = append(m.pendingRequests, req)
	}

	// all requests are processed, except those waiting for another chain read
	m.requests = reread

	m.log.Debug("requests valid", "num_requests", len(manifests))

	if len(manifests) > 0 {
		// XXX: only one version means only one valid manifest
		m.manifests = append(m.manifests, manifests[0])
	}
}

func (m *manager) validateRequest(req manifestRequest) error {
	select {
	case <-req.ctx.Done():
		return req.ctx.Err()
	default:
	}

	// Any failure from Manifest.Validate is a client-side validation error.
	// Ensure it is classified as such even if the upstream error is not already
	// tagged with ErrInvalidManifest, so the gateway returns 4xx rather than 500.
	if err := req.value.Manifest.Validate(); err != nil {
		if !errors.Is(err, maniv2beta2.ErrInvalidManifest) {
			err = fmt.Errorf("%w: %w", maniv2beta2.ErrInvalidManifest, err)
		}
		return err
	}

	// Cap tenant-supplied proxy buffer sizes before they reach the shared gateway.
	if err := validateProxyBufferLimits(req.value.Manifest); err != nil {
		return err
	}

	// ensure that an uploaded manifest matches the hash declared on
	// the Akash Deployment.Version
	version, err := req.value.Manifest.Version()
	if err != nil {
		return err
	}

	if !bytes.Equal(version, m.expected.hash) {
		// A first mismatch only leads to another chain read; after that one it is final.
		logMismatch := m.log.Info
		if req.reread {
			logMismatch = m.log.Error
		}
		logMismatch("deployment version mismatch",
			"expected", hex.EncodeToString(m.expected.hash),
			"expected_height", m.expected.height,
			"got", hex.EncodeToString(version))
		return ErrManifestVersion
	}

	if err = req.value.Manifest.CheckAgainstDeployment(m.data.Groups); err != nil {
		return err
	}

	groupNames := make([]string, 0)

	for _, lease := range m.localLeases {
		groupNames = append(groupNames, lease.Group.GroupSpec.Name)
	}

	// Check that hostnames are not in use
	if err = m.checkHostnamesForManifest(req.value.Manifest, groupNames); err != nil {
		return err
	}

	return nil
}

func (m *manager) checkHostnamesForManifest(requestManifest maniv2beta2.Manifest, groupNames []string) error {
	// Check if the hostnames are available. Do not block forever
	ownerAddr, err := m.data.GetDeployment().ID.GetOwnerAddress()
	if err != nil {
		return err
	}

	allHostnames := make([]string, 0)

	for _, mgroup := range requestManifest.GetGroups() {
		for _, groupName := range groupNames {
			// Only check leases with a matching deployment ID & group name
			if groupName != mgroup.GetName() {
				continue
			}

			allHostnames = append(allHostnames, AllHostnamesOfManifestGroup(mgroup)...)
			if !m.config.HTTPServicesRequireAtLeastOneHost {
				continue
			}
			// For each service that exposes via an Ingress, then require a hostname
			for _, service := range mgroup.Services {
				for _, expose := range service.Expose {
					if expose.IsIngress() && len(expose.Hosts) == 0 {
						return fmt.Errorf("%w: service %q exposed on %d:%s must have a hostname",
							errManifestRejected, service.Name, expose.GetExternalPort(), expose.Proto)
					}
				}
			}
		}
	}

	return m.hostnameService.CanReserveHostnames(allHostnames, ownerAddr)
}

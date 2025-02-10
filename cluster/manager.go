package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tendermint/tendermint/libs/log"

	"github.com/avast/retry-go/v4"
	"github.com/boz/go-lifecycle"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/pubsub"

	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	clusterutil "github.com/akash-network/provider/cluster/util"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/manifest"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
)

const (
	dsDeployActive     deploymentState = "deploy-active"
	dsDeployPending    deploymentState = "deploy-pending"
	dsDeployComplete   deploymentState = "deploy-complete"
	dsTeardownActive   deploymentState = "teardown-active"
	dsTeardownPending  deploymentState = "teardown-pending"
	dsTeardownComplete deploymentState = "teardown-complete"
)

const uncleanShutdownGracePeriod = 30 * time.Second

type deploymentState string

var (
	deploymentCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_deployment",
	}, []string{"action", "result"})

	monitorCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_deployment_monitor",
	}, []string{"action"})

	ErrLeaseInactive = errors.New("inactive Lease")
)

type deploymentManager struct {
	bus                 pubsub.Bus
	client              Client
	session             session.Session
	state               deploymentState
	deployment          ctypes.IDeployment
	monitor             *deploymentMonitor
	wg                  sync.WaitGroup
	updatech            chan ctypes.IDeployment
	teardownch          chan struct{}
	currentHostnames    map[string]struct{}
	log                 log.Logger
	lc                  lifecycle.Lifecycle
	hostnameService     ctypes.HostnameServiceClient
	config              Config
	isNewLease          bool
	serviceShuttingDown <-chan struct{}
}

func newDeploymentManager(s *service, deployment ctypes.IDeployment, isNewLease bool) *deploymentManager {
	lid := deployment.LeaseID()
	mgroup := deployment.ManifestGroup()

	logger := s.log.With("cmp", "deployment-manager", "lease", lid, "manifest-group", mgroup.GetName())

	dm := &deploymentManager{
		bus:                 s.bus,
		client:              s.client,
		session:             s.session,
		state:               dsDeployActive,
		deployment:          deployment,
		wg:                  sync.WaitGroup{},
		updatech:            make(chan ctypes.IDeployment),
		teardownch:          make(chan struct{}),
		log:                 logger,
		lc:                  lifecycle.New(),
		hostnameService:     s.HostnameService(),
		config:              s.config,
		serviceShuttingDown: s.lc.ShuttingDown(),
		isNewLease:          isNewLease,
		currentHostnames:    make(map[string]struct{}),
	}

	go dm.lc.WatchChannel(s.lc.ShuttingDown())
	go dm.run(context.Background())

	go func() {
		<-dm.lc.Done()
		dm.log.Debug("sending manager into channel")
		s.managerch <- dm
	}()

	err := s.bus.Publish(event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: isNewLease})
	if err != nil {
		s.log.Error("unable to publish LeaseAddFundsMonitor event", "error", err, "lease", lid)
	}

	return dm
}

func (dm *deploymentManager) update(deployment ctypes.IDeployment) error {
	select {
	case dm.updatech <- deployment:
		return nil
	case <-dm.lc.ShuttingDown():
		return ErrNotRunning
	}
}

func (dm *deploymentManager) teardown() error {
	select {
	case dm.teardownch <- struct{}{}:
		return nil
	case <-dm.lc.ShuttingDown():
		return ErrNotRunning
	}
}

func (dm *deploymentManager) handleUpdate(ctx context.Context) <-chan error {
	switch dm.state {
	case dsDeployActive:
		dm.state = dsDeployPending
	case dsDeployComplete:
		// start update
		return dm.startDeploy(ctx)
	case dsDeployPending, dsTeardownActive, dsTeardownPending, dsTeardownComplete:
		// do nothing
	}

	return nil
}

func (dm *deploymentManager) run(ctx context.Context) {
	defer dm.lc.ShutdownCompleted()
	var shutdownErr error

	runch := dm.startDeploy(ctx)

	defer func() {
		err := dm.hostnameService.ReleaseHostnames(dm.deployment.LeaseID())
		if err != nil {
			dm.log.Error("failed releasing hostnames", "err", err)
		}
		dm.log.Debug("hostnames released")
	}()

	var teardownErr error

loop:
	for {
		select {
		case shutdownErr = <-dm.lc.ShutdownRequest():
			dm.log.Debug("received shutdown request", "err", shutdownErr)
			break loop
		case deployment := <-dm.updatech:
			dm.deployment = deployment
			newch := dm.handleUpdate(ctx)
			if newch != nil {
				runch = newch
			}

		case result := <-runch:
			runch = nil
			if result != nil {
				dm.log.Error("execution error", "state", dm.state, "err", result)
			}
			switch dm.state {
			case dsDeployActive:
				if result != nil {
					// Run the teardown code to get rid of anything created that might be hanging out
					runch = dm.startTeardown()
				} else {
					dm.log.Debug("deploy complete")
					dm.state = dsDeployComplete
					dm.startMonitor()
				}
			case dsDeployPending:
				if result != nil {
					break loop
				}
				// start update
				runch = dm.startDeploy(ctx)
			case dsDeployComplete:
				panic(fmt.Sprintf("INVALID STATE: runch read on %v", dm.state))
			case dsTeardownActive:
				teardownErr = result
				dm.state = dsTeardownComplete
				dm.log.Debug("teardown complete")
				break loop
			case dsTeardownPending:
				// start teardown
				runch = dm.startTeardown()
			case dsTeardownComplete:
				panic(fmt.Sprintf("INVALID STATE: runch read on %v", dm.state))
			}

		case <-dm.teardownch:
			dm.log.Debug("teardown request")
			dm.stopMonitor()
			switch dm.state {
			case dsDeployActive:
				dm.state = dsTeardownPending
			case dsDeployPending:
				dm.state = dsTeardownPending
			case dsDeployComplete:
				// start teardown
				runch = dm.startTeardown()
			case dsTeardownActive, dsTeardownPending, dsTeardownComplete:
			}
		}
	}

	dm.log.Debug("shutting down")
	dm.lc.ShutdownInitiated(shutdownErr)
	if runch != nil {
		<-runch
		dm.log.Debug("read from runch during shutdown")
	}

	dm.log.Debug("waiting on dm.wg")
	dm.wg.Wait()

	// if dm.isNewLease && (dm.state < dsDeployComplete) {
	// 	dm.log.Info("shutting down unclean, running teardown now")
	// 	ctx, cancel := context.WithTimeout(context.Background(), uncleanShutdownGracePeriod)
	// 	defer cancel()
	// 	teardownErr = dm.doTeardown(ctx)
	// }

	if teardownErr != nil {
		dm.log.Error("lease teardown failed", "err", teardownErr)
	}

	dm.log.Info("shutdown complete")
}

func (dm *deploymentManager) startMonitor() {
	dm.wg.Add(1)
	dm.monitor = newDeploymentMonitor(dm)
	monitorCounter.WithLabelValues("start").Inc()
	go func(m *deploymentMonitor) {
		defer dm.wg.Done()
		<-m.done()
	}(dm.monitor)
}

func (dm *deploymentManager) stopMonitor() {
	if dm.monitor != nil {
		monitorCounter.WithLabelValues("stop").Inc()
		dm.monitor.shutdown()
	}
}

func (dm *deploymentManager) startDeploy(ctx context.Context) <-chan error {
	dm.stopMonitor()
	dm.state = dsDeployActive

	chErr := make(chan error, 1)

	go func() {
		hostnames, endpoints, err := dm.doDeploy(ctx)
		if err != nil {
			chErr <- err
			return
		}

		if len(hostnames) != 0 {
			// Some hostnames have been withheld
			dm.log.Info("hostnames withheld from deployment", "cnt", len(hostnames), "lease", dm.deployment.LeaseID())
		}

		if len(endpoints) != 0 {
			// Some endpoints have been withheld
			dm.log.Info("endpoints withheld from deployment", "cnt", len(endpoints), "lease", dm.deployment.LeaseID())
		}

		groupCopy := *dm.deployment.ManifestGroup()
		ev := event.ClusterDeployment{
			LeaseID: dm.deployment.LeaseID(),
			Group:   &groupCopy,
			Status:  event.ClusterDeploymentUpdated,
		}
		err = dm.bus.Publish(ev)
		if err != nil {
			dm.log.Error("failed publishing event", "err", err)
		}

		close(chErr)
	}()

	return chErr
}

func (dm *deploymentManager) startTeardown() <-chan error {
	dm.stopMonitor()
	dm.state = dsTeardownActive
	return dm.do(func() error {
		// Don't use a context tied to the lifecycle, as we don't want to cancel Kubernetes operations
		return dm.doTeardown(context.Background())
	})
}

type serviceExposeWithServiceName struct {
	expose mani.ServiceExpose
	name   string
}

func (dm *deploymentManager) doDeploy(ctx context.Context) ([]string, []string, error) {
	cleanupHelper := newDeployCleanupHelper(dm.deployment.LeaseID(), dm.client, dm.log)
	var err error
	ctx, cancel := context.WithCancel(context.Background())

	// Weird hack to tie this context to the lifecycle of the parent service, so this doesn't
	// block forever or anything like that
	go func() {
		select {
		case <-dm.serviceShuttingDown:
			cancel()
		case <-ctx.Done():
		}
	}()

	defer func() {
		// TODO - run on an isolated context
		cleanupHelper.purgeAll(ctx)
		cancel()
	}()

	if err = dm.checkLeaseActive(ctx); err != nil {
		return nil, nil, err
	}

	currentIPs, err := dm.client.GetDeclaredIPs(ctx, dm.deployment.LeaseID())
	if err != nil {
		return nil, nil, err
	}

	// Either reserve the hostnames, or confirm that they already are held
	allHostnames := manifest.AllHostnamesOfManifestGroup(*dm.deployment.ManifestGroup())
	withheldHostnames, err := dm.hostnameService.ReserveHostnames(ctx, allHostnames, dm.deployment.LeaseID())

	if err != nil {
		deploymentCounter.WithLabelValues("reserve-hostnames", "err").Inc()
		dm.log.Error("deploy hostname reservation error", "state", dm.state, "err", err)
		return nil, nil, err
	}
	deploymentCounter.WithLabelValues("reserve-hostnames", "success").Inc()

	dm.log.Info("hostnames withheld", "cnt", len(withheldHostnames))

	hostnamesInThisRequest := make(map[string]struct{})
	for _, hostname := range allHostnames {
		hostnamesInThisRequest[hostname] = struct{}{}
	}

	// Figure out what hostnames were removed from the manifest if any
	for hostnameInUse := range dm.currentHostnames {
		_, stillInUse := hostnamesInThisRequest[hostnameInUse]
		if !stillInUse {
			cleanupHelper.addHostname(hostnameInUse)
		}
	}

	// Don't use a context tied to the lifecycle, as we don't want to cancel Kubernetes operations
	deployCtx := fromctx.ApplyToContext(context.Background(), dm.config.ClusterSettings)

	err = dm.client.Deploy(deployCtx, dm.deployment)
	label := "success"
	if err != nil {
		label = "fail"
	}
	deploymentCounter.WithLabelValues("deploy", label).Inc()
	if err != nil {
		dm.log.Error("deploying workload", "err", err.Error())
		return nil, nil, err
	}

	// Figure out what hostnames to declare
	blockedHostnames := make(map[string]struct{})
	for _, hostname := range withheldHostnames {
		blockedHostnames[hostname] = struct{}{}
	}
	hosts := make(map[string]mani.ServiceExpose)
	leasedIPs := make([]serviceExposeWithServiceName, 0)
	hostToServiceName := make(map[string]string)

	ipsInThisRequest := make(map[string]serviceExposeWithServiceName)
	// clear this out so it gets repopulated
	dm.currentHostnames = make(map[string]struct{})
	// Iterate over each entry, extracting the ingress services & leased IPs
	for _, service := range dm.deployment.ManifestGroup().Services {
		for _, expose := range service.Expose {
			if expose.IsIngress() {
				if dm.config.DeploymentIngressStaticHosts {
					uid := manifest.IngressHost(dm.deployment.LeaseID(), service.Name)
					host := fmt.Sprintf("%s.%s", uid, dm.config.DeploymentIngressDomain)
					hosts[host] = expose
					hostToServiceName[host] = service.Name
				}

				for _, host := range expose.Hosts {
					_, blocked := blockedHostnames[host]
					if !blocked {
						dm.currentHostnames[host] = struct{}{}
						hosts[host] = expose
						hostToServiceName[host] = service.Name
					}
				}
			}

			if expose.Global && len(expose.IP) != 0 {
				v := serviceExposeWithServiceName{expose: expose, name: service.Name}
				leasedIPs = append(leasedIPs, v)
				sharingKey := clusterutil.MakeIPSharingKey(dm.deployment.LeaseID(), expose.IP)
				ipsInThisRequest[sharingKey] = v
			}
		}
	}

	for _, currentIP := range currentIPs {
		// Check if the IP exists in the compute cluster but not in the presently used set of IPs
		_, stillInUse := ipsInThisRequest[currentIP.SharingKey]
		if !stillInUse {
			proto, err := mani.ParseServiceProtocol(currentIP.Protocol)
			if err != nil {
				return withheldHostnames, nil, err
			}
			cleanupHelper.addIP(currentIP.ServiceName, currentIP.ExternalPort, proto)
		}
	}

	for host, serviceExpose := range hosts {
		externalPort := uint32(serviceExpose.GetExternalPort()) // nolint: gosec
		err = dm.client.DeclareHostname(ctx, dm.deployment.LeaseID(), host, hostToServiceName[host], externalPort)
		if err != nil {
			// TODO - counter
			return withheldHostnames, nil, err
		}
	}

	withheldEndpoints := make([]string, 0)
	for _, serviceExpose := range leasedIPs {
		endpointName := serviceExpose.expose.IP
		sharingKey := clusterutil.MakeIPSharingKey(dm.deployment.LeaseID(), endpointName)

		externalPort := serviceExpose.expose.GetExternalPort()
		port := serviceExpose.expose.Port

		err = dm.client.DeclareIP(ctx, dm.deployment.LeaseID(), serviceExpose.name, port, uint32(externalPort), serviceExpose.expose.Proto, sharingKey, false) // nolint: gosec
		if err != nil {
			if !errors.Is(err, kubeclienterrors.ErrAlreadyExists) {
				dm.log.Error("failed adding IP declaration", "service", serviceExpose.name, "port", externalPort, "endpoint", serviceExpose.expose.IP, "err", err)
				return withheldHostnames, nil, err
			}
			dm.log.Info("IP declaration already exists", "service", serviceExpose.name, "port", externalPort, "endpoint", serviceExpose.expose.IP, "err", err)
			withheldEndpoints = append(withheldEndpoints, sharingKey)

		} else {
			dm.log.Debug("added IP declaration", "service", serviceExpose.name, "port", externalPort, "endpoint", serviceExpose.expose.IP)
		}
	}

	return withheldHostnames, withheldEndpoints, nil
}

func (dm *deploymentManager) getCleanupRetryOpts(ctx context.Context) []retry.Option {
	retryFn := func(err error) bool {
		isCanceled := errors.Is(err, context.Canceled)
		isDeadlineExceeded := errors.Is(err, context.DeadlineExceeded)
		return !isCanceled && !isDeadlineExceeded
	}
	return []retry.Option{
		retry.Attempts(50),
		retry.Delay(100 * time.Millisecond),
		retry.MaxDelay(3000 * time.Millisecond),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.RetryIf(retryFn),
		retry.Context(ctx),
	}
}

func (dm *deploymentManager) doTeardown(ctx context.Context) error {
	const teardownActivityCount = 3
	teardownResults := make(chan error, teardownActivityCount)

	go func() {
		result := retry.Do(func() error {
			err := dm.client.TeardownLease(ctx, dm.deployment.LeaseID())
			if err != nil {
				dm.log.Error("lease teardown failed", "err", err)
			}
			return err
		}, dm.getCleanupRetryOpts(ctx)...)

		label := "success"
		if result != nil {
			label = "fail"
		}
		deploymentCounter.WithLabelValues("teardown", label).Inc()
		teardownResults <- result
	}()

	go func() {
		result := retry.Do(func() error {
			err := dm.client.PurgeDeclaredHostnames(ctx, dm.deployment.LeaseID())
			if err != nil {
				dm.log.Error("purge declared hostname failure", "err", err)
			}
			return err
		}, dm.getCleanupRetryOpts(ctx)...)
		// TODO - counter

		if result == nil {
			dm.log.Debug("purged hostnames")
		}
		teardownResults <- result
	}()

	go func() {
		result := retry.Do(func() error {
			err := dm.client.PurgeDeclaredIPs(ctx, dm.deployment.LeaseID())
			if err != nil {
				dm.log.Error("purge declared ips failure", "err", err)
			}
			return err
		}, dm.getCleanupRetryOpts(ctx)...)
		// TODO - counter

		if result == nil {
			dm.log.Debug("purged ips")
		}
		teardownResults <- result
	}()

	var firstError error
	for i := 0; i != teardownActivityCount; i++ {
		select {
		case err := <-teardownResults:
			if err != nil && firstError == nil {
				firstError = err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return firstError
}

func (dm *deploymentManager) checkLeaseActive(ctx context.Context) error {
	var lease *mtypes.QueryLeaseResponse

	err := retry.Do(func() error {
		var err error
		lease, err = dm.session.Client().Query().Lease(ctx, &mtypes.QueryLeaseRequest{
			ID: dm.deployment.LeaseID(),
		})
		if err != nil {
			dm.log.Error("lease query failed", "err")
		}
		return err
	},
		retry.Attempts(50),
		retry.Delay(100*time.Millisecond),
		retry.MaxDelay(3000*time.Millisecond),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true))

	if err != nil {
		return err
	}

	if lease.GetLease().State != mtypes.LeaseActive {
		dm.log.Error("lease not active, not deploying")
		return fmt.Errorf("%w: %s", ErrLeaseInactive, dm.deployment.LeaseID())
	}

	return nil
}

func (dm *deploymentManager) do(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- fn()
	}()
	return ch
}

func TieContextToChannel(parentCtx context.Context, donech <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parentCtx)

	go func() {
		select {
		case <-donech:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, cancel
}

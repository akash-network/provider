package cluster

import (
	"context"
	"math/rand"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	"github.com/boz/go-lifecycle"

	"cosmossdk.io/log"
	sdk "github.com/cosmos/cosmos-sdk/types"

	aclient "pkg.akt.dev/go/node/client/v1beta3"
	"pkg.akt.dev/go/util/pubsub"
	"pkg.akt.dev/node/util/runner"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
)

var (
	deploymentHealthCheckCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_deployment_monitor_health",
	}, []string{"state"})
)

type deploymentMonitor struct {
	bus     pubsub.Bus
	session session.Session
	client  Client

	deployment ctypes.IDeployment

	attempts uint
	log      log.Logger
	lc       lifecycle.Lifecycle

	config Config
}

func newDeploymentMonitor(dm *deploymentManager) *deploymentMonitor {
	m := &deploymentMonitor{
		bus:        dm.bus,
		session:    dm.session,
		client:     dm.client,
		deployment: dm.deployment,
		log:        dm.log.With("cmp", "deployment-monitor"),
		lc:         lifecycle.New(),
		config:     dm.config,
	}

	go m.lc.WatchChannel(dm.lc.ShuttingDown())
	go m.run()

	return m
}

func (m *deploymentMonitor) shutdown() {
	m.lc.ShutdownAsync(nil)
}

func (m *deploymentMonitor) done() <-chan struct{} {
	return m.lc.Done()
}

func (m *deploymentMonitor) run() {
	defer m.lc.ShutdownCompleted()
	ctx, cancel := context.WithCancel(context.Background())

	var (
		runch   <-chan runner.Result
		closech <-chan runner.Result
	)

	tickch := m.scheduleRetry()

	prevStatus := event.ClusterDeploymentUnknown

loop:
	for {
		select {
		case err := <-m.lc.ShutdownRequest():
			m.lc.ShutdownInitiated(err)
			break loop

		case <-tickch:
			tickch = nil
			runch = m.runCheck(ctx)

		case result := <-runch:
			runch = nil

			if err := result.Error(); err != nil {
				deploymentHealthCheckCounter.WithLabelValues("err").Inc()
				m.log.Error("monitor check", "err", err)
			}

			var currStatus event.ClusterDeploymentStatus

			healthy := result.Value().(bool)

			if healthy {
				currStatus = event.ClusterDeploymentDeployed

				m.attempts = 0
				tickch = m.scheduleHealthcheck()

				deploymentHealthCheckCounter.WithLabelValues("up").Inc()
			} else {
				currStatus = event.ClusterDeploymentPending

				deploymentHealthCheckCounter.WithLabelValues("down").Inc()
				m.log.Info("check result", "ok", false, "attempt", m.attempts)
			}

			if currStatus != prevStatus {
				m.publishStatus(currStatus)
				prevStatus = currStatus
			}

			if !healthy {
				if m.attempts <= m.config.MonitorMaxRetries {
					// unhealthy.  retry
					tickch = m.scheduleRetry()
					break
				}

				m.log.Error("deployment failed.  closing lease.")
				deploymentHealthCheckCounter.WithLabelValues("failed").Inc()
				closech = m.runCloseLease(ctx)
			}
		case <-closech:
			closech = nil
		}
	}
	cancel()

	if runch != nil {
		<-runch
	}

	if closech != nil {
		<-closech
	}
}

func (m *deploymentMonitor) runCheck(ctx context.Context) <-chan runner.Result {
	m.attempts++
	return runner.Do(func() runner.Result {
		return runner.NewResult(m.doCheck(ctx))
	})
}

func (m *deploymentMonitor) doCheck(ctx context.Context) (bool, error) {
	ctx = fromctx.ApplyToContext(ctx, m.config.ClusterSettings)

	status, err := m.client.LeaseStatus(ctx, m.deployment.LeaseID())

	if err != nil {
		m.log.Error("lease status", "err", err)
		return false, err
	}

	badsvc := 0

	for _, spec := range m.deployment.ManifestGroup().Services {
		service, foundService := status[spec.Name]
		if foundService {
			if uint32(service.Available) < spec.Count { // nolint: gosec
				badsvc++
				m.log.Debug("service available replicas below target",
					"service", spec.Name,
					"available", service.Available,
					"target", spec.Count,
				)
			}
		}

		if !foundService {
			badsvc++
			m.log.Debug("service status not found", "service", spec.Name)
		}
	}

	return badsvc == 0, nil
}

func (m *deploymentMonitor) runCloseLease(ctx context.Context) <-chan runner.Result {
	return runner.Do(func() runner.Result {
		// TODO: retry, timeout
		msg := &mvbeta.MsgCloseBid{
			ID: m.deployment.LeaseID().BidID(),
		}
		res, err := m.session.Client().Tx().BroadcastMsgs(ctx, []sdk.Msg{msg}, aclient.WithResultCodeAsError())
		if err != nil {
			m.log.Error("closing deployment", "err", err)
		} else {
			m.log.Info("bidding on lease closed")
		}
		return runner.NewResult(res, err)
	})
}

func (m *deploymentMonitor) publishStatus(status event.ClusterDeploymentStatus) {
	if err := m.bus.Publish(event.ClusterDeployment{
		LeaseID: m.deployment.LeaseID(),
		Group:   m.deployment.ManifestGroup(),
		Status:  status,
	}); err != nil {
		m.log.Error("publishing manifest group deployed event", "err", err, "status", status)
	}
}

func (m *deploymentMonitor) scheduleRetry() <-chan time.Time {
	return m.schedule(m.config.MonitorRetryPeriod, m.config.MonitorRetryPeriodJitter)
}

func (m *deploymentMonitor) scheduleHealthcheck() <-chan time.Time {
	return m.schedule(m.config.MonitorHealthcheckPeriod, m.config.MonitorHealthcheckPeriodJitter)
}

func (m *deploymentMonitor) schedule(minTime, jitter time.Duration) <-chan time.Time {
	period := minTime + time.Duration(rand.Int63n(int64(jitter))) // nolint: gosec
	return time.After(period)
}

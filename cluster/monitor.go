package cluster

import (
	"context"
	"math/rand"
	"time"

	aclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/boz/go-lifecycle"
	"github.com/tendermint/tendermint/libs/log"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/pubsub"
	"github.com/akash-network/node/util/runner"

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

loop:
	for {
		select {
		case err := <-m.lc.ShutdownRequest():
			m.log.Debug("shutting down")
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

			ok := result.Value().(bool)

			m.log.Info("check result", "ok", ok, "attempt", m.attempts)

			if ok {
				// healthy
				m.attempts = 0
				tickch = m.scheduleHealthcheck()
				m.publishStatus(event.ClusterDeploymentDeployed)
				deploymentHealthCheckCounter.WithLabelValues("up").Inc()

				break
			} else {
				deploymentHealthCheckCounter.WithLabelValues("down").Inc()
			}

			m.publishStatus(event.ClusterDeploymentPending)

			if m.attempts <= m.config.MonitorMaxRetries {
				// unhealthy.  retry
				tickch = m.scheduleRetry()
				break
			}

			m.log.Error("deployment failed.  closing lease.")
			deploymentHealthCheckCounter.WithLabelValues("failed").Inc()
			closech = m.runCloseLease(ctx)

		case <-closech:
			closech = nil
		}
	}
	cancel()

	if runch != nil {
		m.log.Debug("read runch")
		<-runch
	}

	if closech != nil {
		m.log.Debug("read closech")
		<-closech
	}

	// TODO
	// Check that we got here
	m.log.Debug("shutdown complete")
}

func (m *deploymentMonitor) runCheck(ctx context.Context) <-chan runner.Result {
	m.attempts++
	m.log.Debug("running check", "attempt", m.attempts)
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
			if uint32(service.Available) < spec.Count {
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
		msg := &mtypes.MsgCloseBid{
			BidID: m.deployment.LeaseID().BidID(),
		}
		res, err := m.session.Client().Tx().Broadcast(ctx, []sdk.Msg{msg}, aclient.WithResultCodeAsError())
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

func (m *deploymentMonitor) schedule(min, jitter time.Duration) <-chan time.Time {
	period := min + time.Duration(rand.Int63n(int64(jitter))) // nolint: gosec
	return time.After(period)
}

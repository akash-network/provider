package cluster

import (
	"testing"

	"github.com/boz/go-lifecycle"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	"github.com/akash-network/node/pubsub"
	"github.com/akash-network/node/testutil"

	"github.com/akash-network/provider/cluster/mocks"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
)

func TestMonitorInstantiate(t *testing.T) {
	myLog := testutil.Logger(t)
	bus := pubsub.NewBus()

	client := &mocks.Client{}
	deployment := &ctypes.Deployment{
		Lid:    testutil.LeaseID(t),
		MGroup: &manifest.Group{},
	}

	statusResult := &ctypes.LeaseStatus{}
	client.On("LeaseStatus", mock.Anything, deployment.LeaseID()).Return(statusResult, nil)
	mySession := session.New(myLog, nil, nil, -1)

	lc := lifecycle.New()
	myDeploymentManager := &deploymentManager{
		bus:        bus,
		session:    mySession,
		client:     client,
		deployment: deployment,
		log:        myLog,
		lc:         lc,
		config:     NewDefaultConfig(),
	}
	monitor := newDeploymentMonitor(myDeploymentManager)
	require.NotNil(t, monitor)

	monitor.lc.Shutdown(nil)
}

func TestMonitorSendsClusterDeploymentPending(t *testing.T) {
	const serviceName = "test"
	myLog := testutil.Logger(t)
	bus := pubsub.NewBus()

	group := &manifest.Group{}
	group.Services = make(manifest.Services, 1)
	group.Services[0].Name = serviceName
	group.Services[0].Expose = make([]manifest.ServiceExpose, 1)
	group.Services[0].Expose[0].ExternalPort = 2000
	group.Services[0].Expose[0].Proto = manifest.TCP
	group.Services[0].Expose[0].Port = 40000
	client := &mocks.Client{}
	deployment := &ctypes.Deployment{
		Lid:    testutil.LeaseID(t),
		MGroup: group,
	}

	statusResult := make(map[string]*ctypes.ServiceStatus)
	client.On("LeaseStatus", mock.Anything, deployment.LeaseID()).Return(statusResult, nil)
	mySession := session.New(myLog, nil, nil, -1)

	sub, err := bus.Subscribe()
	require.NoError(t, err)
	lc := lifecycle.New()
	myDeploymentManager := &deploymentManager{
		bus:        bus,
		session:    mySession,
		client:     client,
		deployment: deployment,
		log:        myLog,
		lc:         lc,
		config:     NewDefaultConfig(),
	}
	monitor := newDeploymentMonitor(myDeploymentManager)
	require.NotNil(t, monitor)

	ev := <-sub.Events()
	result := ev.(event.ClusterDeployment)
	require.Equal(t, deployment.LeaseID(), result.LeaseID)
	require.Equal(t, event.ClusterDeploymentPending, result.Status)

	monitor.lc.Shutdown(nil)
}

func TestMonitorSendsClusterDeploymentDeployed(t *testing.T) {
	const serviceName = "test"
	myLog := testutil.Logger(t)
	bus := pubsub.NewBus()

	group := &manifest.Group{}
	group.Services = make(manifest.Services, 1)
	group.Services[0].Name = serviceName
	group.Services[0].Expose = make([]manifest.ServiceExpose, 1)
	group.Services[0].Expose[0].ExternalPort = 2000
	group.Services[0].Expose[0].Proto = manifest.TCP
	group.Services[0].Expose[0].Port = 40000
	group.Services[0].Count = 3
	client := &mocks.Client{}
	deployment := &ctypes.Deployment{
		Lid:    testutil.LeaseID(t),
		MGroup: group,
	}

	statusResult := make(map[string]*ctypes.ServiceStatus)
	statusResult[serviceName] = &ctypes.ServiceStatus{
		Name:               serviceName,
		Available:          3,
		Total:              3,
		URIs:               nil,
		ObservedGeneration: 0,
		Replicas:           0,
		UpdatedReplicas:    0,
		ReadyReplicas:      0,
		AvailableReplicas:  0,
	}
	client.On("LeaseStatus", mock.Anything, deployment.LeaseID()).Return(statusResult, nil)
	mySession := session.New(myLog, nil, nil, -1)

	sub, err := bus.Subscribe()
	require.NoError(t, err)
	lc := lifecycle.New()
	myDeploymentManager := &deploymentManager{
		bus:        bus,
		session:    mySession,
		client:     client,
		deployment: deployment,
		log:        myLog,
		lc:         lc,
		config:     NewDefaultConfig(),
	}
	monitor := newDeploymentMonitor(myDeploymentManager)
	require.NotNil(t, monitor)

	ev := <-sub.Events()
	result := ev.(event.ClusterDeployment)
	require.Equal(t, deployment.LeaseID(), result.LeaseID)
	require.Equal(t, event.ClusterDeploymentDeployed, result.Status)

	monitor.lc.Shutdown(nil)
}

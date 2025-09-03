package cluster

import (
	"context"
	"io"
	"testing"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	"github.com/boz/go-lifecycle"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/tools/remotecommand"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/pubsub"
	"github.com/akash-network/node/testutil"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	"github.com/akash-network/provider/event"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	"github.com/akash-network/provider/session"
)

// mockClient is a simple mock implementation for testing
type mockClient struct {
	mock.Mock
}

// ReadClient interface methods
func (m *mockClient) LeaseStatus(ctx context.Context, leaseID mtypes.LeaseID) (map[string]*apclient.ServiceStatus, error) {
	args := m.Called(ctx, leaseID)
	return args.Get(0).(map[string]*apclient.ServiceStatus), args.Error(1)
}

func (m *mockClient) ForwardedPortStatus(ctx context.Context, leaseID mtypes.LeaseID) (map[string][]apclient.ForwardedPortStatus, error) {
	args := m.Called(ctx, leaseID)
	return args.Get(0).(map[string][]apclient.ForwardedPortStatus), args.Error(1)
}

func (m *mockClient) LeaseEvents(ctx context.Context, leaseID mtypes.LeaseID, serviceName string, follow bool) (ctypes.EventsWatcher, error) {
	args := m.Called(ctx, leaseID, serviceName, follow)
	return args.Get(0).(ctypes.EventsWatcher), args.Error(1)
}

func (m *mockClient) LeaseLogs(ctx context.Context, leaseID mtypes.LeaseID, serviceName string, follow bool, tailLines *int64) ([]*ctypes.ServiceLog, error) {
	args := m.Called(ctx, leaseID, serviceName, follow, tailLines)
	return args.Get(0).([]*ctypes.ServiceLog), args.Error(1)
}

func (m *mockClient) ServiceStatus(ctx context.Context, leaseID mtypes.LeaseID, serviceName string) (*apclient.ServiceStatus, error) {
	args := m.Called(ctx, leaseID, serviceName)
	return args.Get(0).(*apclient.ServiceStatus), args.Error(1)
}

func (m *mockClient) AllHostnames(ctx context.Context) ([]chostname.ActiveHostname, error) {
	args := m.Called(ctx)
	return args.Get(0).([]chostname.ActiveHostname), args.Error(1)
}

func (m *mockClient) GetManifestGroup(ctx context.Context, leaseID mtypes.LeaseID) (bool, crd.ManifestGroup, error) {
	args := m.Called(ctx, leaseID)
	return args.Bool(0), args.Get(1).(crd.ManifestGroup), args.Error(2)
}

func (m *mockClient) ObserveHostnameState(ctx context.Context) (<-chan chostname.ResourceEvent, error) {
	args := m.Called(ctx)
	return args.Get(0).(<-chan chostname.ResourceEvent), args.Error(1)
}

func (m *mockClient) GetHostnameDeploymentConnections(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	args := m.Called(ctx)
	return args.Get(0).([]chostname.LeaseIDConnection), args.Error(1)
}

func (m *mockClient) ObserveIPState(ctx context.Context) (<-chan cip.ResourceEvent, error) {
	args := m.Called(ctx)
	return args.Get(0).(<-chan cip.ResourceEvent), args.Error(1)
}

func (m *mockClient) GetDeclaredIPs(ctx context.Context, leaseID mtypes.LeaseID) ([]crd.ProviderLeasedIPSpec, error) {
	args := m.Called(ctx, leaseID)
	return args.Get(0).([]crd.ProviderLeasedIPSpec), args.Error(1)
}

// Client interface methods
func (m *mockClient) Deploy(ctx context.Context, deployment ctypes.IDeployment) error {
	args := m.Called(ctx, deployment)
	return args.Error(0)
}

func (m *mockClient) TeardownLease(ctx context.Context, leaseID mtypes.LeaseID) error {
	args := m.Called(ctx, leaseID)
	return args.Error(0)
}

func (m *mockClient) Deployments(ctx context.Context) ([]ctypes.IDeployment, error) {
	args := m.Called(ctx)
	return args.Get(0).([]ctypes.IDeployment), args.Error(1)
}

func (m *mockClient) Exec(ctx context.Context, leaseID mtypes.LeaseID, service string, podIndex uint, cmd []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, tty bool, tsq remotecommand.TerminalSizeQueue) (ctypes.ExecResult, error) {
	args := m.Called(ctx, leaseID, service, podIndex, cmd, stdin, stdout, stderr, tty, tsq)
	return args.Get(0).(ctypes.ExecResult), args.Error(1)
}

func (m *mockClient) ConnectHostnameToDeployment(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	args := m.Called(ctx, directive)
	return args.Error(0)
}

func (m *mockClient) RemoveHostnameFromDeployment(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	args := m.Called(ctx, hostname, leaseID, allowMissing)
	return args.Error(0)
}

func (m *mockClient) DeclareHostname(ctx context.Context, leaseID mtypes.LeaseID, host string, serviceName string, externalPort uint32) error {
	args := m.Called(ctx, leaseID, host, serviceName, externalPort)
	return args.Error(0)
}

func (m *mockClient) PurgeDeclaredHostnames(ctx context.Context, leaseID mtypes.LeaseID) error {
	args := m.Called(ctx, leaseID)
	return args.Error(0)
}

func (m *mockClient) PurgeDeclaredHostname(ctx context.Context, leaseID mtypes.LeaseID, hostname string) error {
	args := m.Called(ctx, leaseID, hostname)
	return args.Error(0)
}

func (m *mockClient) DeclareIP(ctx context.Context, leaseID mtypes.LeaseID, serviceName string, port uint32, externalPort uint32, proto manifest.ServiceProtocol, sharingKey string, overwrite bool) error {
	args := m.Called(ctx, leaseID, serviceName, port, externalPort, proto, sharingKey, overwrite)
	return args.Error(0)
}

func (m *mockClient) PurgeDeclaredIP(ctx context.Context, leaseID mtypes.LeaseID, serviceName string, externalPort uint32, proto manifest.ServiceProtocol) error {
	args := m.Called(ctx, leaseID, serviceName, externalPort, proto)
	return args.Error(0)
}

func (m *mockClient) PurgeDeclaredIPs(ctx context.Context, leaseID mtypes.LeaseID) error {
	args := m.Called(ctx, leaseID)
	return args.Error(0)
}

func (m *mockClient) KubeVersion() (*version.Info, error) {
	args := m.Called()
	return args.Get(0).(*version.Info), args.Error(1)
}

func TestMonitorInstantiate(t *testing.T) {
	myLog := testutil.Logger(t)
	bus := pubsub.NewBus()

	client := &mockClient{}
	deployment := &ctypes.Deployment{
		Lid:    testutil.LeaseID(t),
		MGroup: &manifest.Group{},
	}

	statusResult := make(map[string]*apclient.ServiceStatus)
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
	client := &mockClient{}
	deployment := &ctypes.Deployment{
		Lid:    testutil.LeaseID(t),
		MGroup: group,
	}

	statusResult := make(map[string]*apclient.ServiceStatus)
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
	client := &mockClient{}
	deployment := &ctypes.Deployment{
		Lid:    testutil.LeaseID(t),
		MGroup: group,
	}

	statusResult := make(map[string]*apclient.ServiceStatus)
	statusResult[serviceName] = &apclient.ServiceStatus{
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

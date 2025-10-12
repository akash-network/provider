package builder

import (
	"strconv"
	"testing"

	manitypes "github.com/akash-network/akash-api/go/manifest/v2beta2"
	"github.com/akash-network/node/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/akash-network/provider/cluster/kube/builder/mocks"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

const testKubeClientNs = "nstest1111"

func TestLidNsSanity(t *testing.T) {
	log := testutil.Logger(t)
	settings := NewDefaultSettings()

	cdep := &ClusterDeployment{
		Lid:   testutil.LeaseID(t),
		Group: &manitypes.Group{},
		Sparams: crd.ClusterSettings{
			SchedulerParams: []*crd.SchedulerParams{},
		},
	}

	mb := BuildManifest(log, settings, testKubeClientNs, cdep)
	require.Equal(t, testKubeClientNs, mb.NS())

	m, err := mb.Create()
	require.NoError(t, err)
	require.Equal(t, m.Spec.LeaseID.DSeq, strconv.FormatUint(cdep.Lid.DSeq, 10))

	require.Equal(t, LidNS(cdep.Lid), m.Name)
}

// func TestNetworkPolicies(t *testing.T) {
// 	leaseID := testutil.LeaseID(t)
//
// 	g := &manitypes.Group{}
// 	settings := NewDefaultSettings()
// 	np := BuildNetPol(NewDefaultSettings(), leaseID, g)
//
// 	// disabled
// 	netPolicies, err := np.Create()
// 	assert.NoError(t, err)
// 	assert.Len(t, netPolicies, 0)
//
// 	// enabled
// 	settings.NetworkPoliciesEnabled = true
// 	np = BuildNetPol(settings, leaseID, g)
// 	netPolicies, err = np.Create()
// 	assert.NoError(t, err)
// 	assert.Len(t, netPolicies, 1)
//
// 	pol0 := netPolicies[0]
// 	assert.Equal(t, pol0.Name, "akash-deployment-restrictions")
//
// 	// Change the DSeq ID
// 	np.DSeq = uint64(100)
// 	k := akashNetworkNamespace
// 	ns := LidNS(np.lid)
// 	updatedNetPol, err := np.Update(netPolicies[0])
// 	assert.NoError(t, err)
// 	updatedNS := updatedNetPol.Labels[k]
// 	assert.Equal(t, ns, updatedNS)
// }

func TestGlobalServiceBuilder(t *testing.T) {
	myLog := testutil.Logger(t)
	mySettings := NewDefaultSettings()

	mani := &crd.Manifest{
		Spec: crd.ManifestSpec{
			Group: crd.ManifestGroup{
				Name: "testgroup",
				Services: []crd.ManifestService{
					{
						Name:            "myservice",
						Image:           "",
						Command:         nil,
						Args:            nil,
						Env:             nil,
						Resources:       crd.Resources{},
						Count:           0,
						Expose:          nil,
						Params:          nil,
						SchedulerParams: nil,
						Credentials:     nil,
					},
				},
			},
		},
	}

	group, sparams, err := mani.Spec.Group.FromCRD()
	require.NoError(t, err)

	cdep := &ClusterDeployment{
		Lid:   testutil.LeaseID(t),
		Group: &group,
		Sparams: crd.ClusterSettings{
			SchedulerParams: sparams,
		},
	}

	workload, err := NewWorkloadBuilder(myLog, mySettings, cdep, mani, 0)
	require.NoError(t, err)

	// This test has no expose directives, so AllocatePorts should not be called
	mockAllocator := mocks.NewServicePortAllocator(t)
	serviceBuilder, err := BuildService(workload, true, mockAllocator)
	require.NoError(t, err)
	require.NotNil(t, serviceBuilder)
	// Should have name ending with suffix
	require.Equal(t, "myservice-np", serviceBuilder.Name())
	// Should not have any work to do
	require.False(t, serviceBuilder.Any())
}

func TestLocalServiceBuilder(t *testing.T) {
	myLog := testutil.Logger(t)
	mySettings := NewDefaultSettings()

	mani := &crd.Manifest{
		Spec: crd.ManifestSpec{
			Group: crd.ManifestGroup{
				Name: "testgroup",
				Services: []crd.ManifestService{
					{
						Name:            "myservice",
						Image:           "",
						Command:         nil,
						Args:            nil,
						Env:             nil,
						Resources:       crd.Resources{},
						Count:           0,
						Expose:          nil,
						Params:          nil,
						SchedulerParams: nil,
						Credentials:     nil,
					},
				},
			},
		},
	}

	group, sparams, err := mani.Spec.Group.FromCRD()
	require.NoError(t, err)

	cdep := &ClusterDeployment{
		Lid:   testutil.LeaseID(t),
		Group: &group,
		Sparams: crd.ClusterSettings{
			SchedulerParams: sparams,
		},
	}

	workload, err := NewWorkloadBuilder(myLog, mySettings, cdep, mani, 0)
	require.NoError(t, err)

	serviceBuilder, err := BuildService(workload, false, nil)
	require.NoError(t, err)
	require.NotNil(t, serviceBuilder)
	// Should have name verbatim
	require.Equal(t, "myservice", serviceBuilder.Name())
	// Should not have any work to do
	require.False(t, serviceBuilder.Any())
}

func TestGlobalServiceBuilderWithoutGlobalServices(t *testing.T) {
	myLog := testutil.Logger(t)

	mySettings := NewDefaultSettings()

	mani := &crd.Manifest{
		Spec: crd.ManifestSpec{
			Group: crd.ManifestGroup{
				Name: "testgroup",
				Services: []crd.ManifestService{
					{
						Name:      "myservice",
						Image:     "",
						Command:   nil,
						Args:      nil,
						Env:       nil,
						Resources: crd.Resources{},
						Count:     0,
						Expose: []crd.ManifestServiceExpose{
							{
								Global: false,
							},
						},
						Params:          nil,
						SchedulerParams: nil,
						Credentials:     nil,
					},
				},
			},
		},
	}

	group, sparams, err := mani.Spec.Group.FromCRD()
	require.NoError(t, err)

	cdep := &ClusterDeployment{
		Lid:   testutil.LeaseID(t),
		Group: &group,
		Sparams: crd.ClusterSettings{
			SchedulerParams: sparams,
		},
	}

	workload, err := NewWorkloadBuilder(myLog, mySettings, cdep, mani, 0)
	require.NoError(t, err)

	// This test has expose directives but only local services (Global: false), so AllocatePorts should not be called for global service builder
	mockAllocator := mocks.NewServicePortAllocator(t)
	serviceBuilder, err := BuildService(workload, true, mockAllocator)
	require.NoError(t, err)

	// Should not have any work to do
	require.False(t, serviceBuilder.Any())
}

func TestGlobalServiceBuilderWithGlobalServices(t *testing.T) {
	myLog := testutil.Logger(t)
	mySettings := NewDefaultSettings()

	mani := &crd.Manifest{
		Spec: crd.ManifestSpec{
			Group: crd.ManifestGroup{
				Name: "testgroup",
				Services: []crd.ManifestService{
					{
						Name:      "myservice",
						Image:     "",
						Command:   nil,
						Args:      nil,
						Env:       nil,
						Resources: crd.Resources{},
						Count:     0,
						Expose: []crd.ManifestServiceExpose{
							{
								Global:       true,
								Proto:        "TCP",
								Port:         1000,
								ExternalPort: 1001,
							},
							{
								Global:       false,
								Proto:        "TCP",
								Port:         2000,
								ExternalPort: 2001,
							},
						},
						Params:          nil,
						SchedulerParams: nil,
						Credentials:     nil,
					},
				},
			},
		},
	}

	group, sparams, err := mani.Spec.Group.FromCRD()
	require.NoError(t, err)

	cdep := &ClusterDeployment{
		Lid:   testutil.LeaseID(t),
		Group: &group,
		Sparams: crd.ClusterSettings{
			SchedulerParams: sparams,
		},
	}

	workload, err := NewWorkloadBuilder(myLog, mySettings, cdep, mani, 0)
	require.NoError(t, err)

	// This test has one global service, so AllocatePorts should be called with count=1
	mockAllocator := mocks.NewServicePortAllocator(t)
	mockAllocator.EXPECT().AllocatePorts("myservice", 1).Return([]int32{30000})
	serviceBuilder, err := BuildService(workload, true, mockAllocator)
	require.NoError(t, err)

	// Should have work to do
	require.True(t, serviceBuilder.Any())

	result, err := serviceBuilder.Create()
	require.NoError(t, err)
	require.Equal(t, result.Spec.Type, corev1.ServiceTypeNodePort)
	ports := result.Spec.Ports
	require.Len(t, ports, 1)
	require.Equal(t, ports[0].Port, int32(1001))
	require.Equal(t, ports[0].TargetPort, intstr.FromInt(1000))
	require.Equal(t, ports[0].Name, "0-1001")
	require.Equal(t, int32(30000), ports[0].NodePort)
}

func TestLocalServiceBuilderWithoutLocalServices(t *testing.T) {
	myLog := testutil.Logger(t)
	mySettings := NewDefaultSettings()

	mani := &crd.Manifest{
		Spec: crd.ManifestSpec{
			Group: crd.ManifestGroup{
				Name: "testgroup",
				Services: []crd.ManifestService{
					{
						Name:      "myservice",
						Image:     "",
						Command:   nil,
						Args:      nil,
						Env:       nil,
						Resources: crd.Resources{},
						Count:     0,
						Expose: []crd.ManifestServiceExpose{
							{
								Global: true,
							},
						},
						Params:          nil,
						SchedulerParams: nil,
						Credentials:     nil,
					},
				},
			},
		},
	}

	group, sparams, err := mani.Spec.Group.FromCRD()
	require.NoError(t, err)

	cdep := &ClusterDeployment{
		Lid:   testutil.LeaseID(t),
		Group: &group,
		Sparams: crd.ClusterSettings{
			SchedulerParams: sparams,
		},
	}

	workload, err := NewWorkloadBuilder(myLog, mySettings, cdep, mani, 0)
	require.NoError(t, err)

	serviceBuilder, err := BuildService(workload, false, nil)
	require.NoError(t, err)

	// Should have work to do
	require.False(t, serviceBuilder.Any())
}

func TestLocalServiceBuilderWithLocalServices(t *testing.T) {
	myLog := testutil.Logger(t)
	mySettings := NewDefaultSettings()

	mani := &crd.Manifest{
		Spec: crd.ManifestSpec{
			Group: crd.ManifestGroup{
				Name: "testgroup",
				Services: []crd.ManifestService{
					{
						Name:      "myservice",
						Image:     "",
						Command:   nil,
						Args:      nil,
						Env:       nil,
						Resources: crd.Resources{},
						Count:     0,
						Expose: []crd.ManifestServiceExpose{
							{
								Global:       true,
								Proto:        "TCP",
								Port:         1000,
								ExternalPort: 1001,
							},
							{
								Global:       false,
								Proto:        "TCP",
								Port:         2000,
								ExternalPort: 2001,
							},
						},
						Params:          nil,
						SchedulerParams: nil,
						Credentials:     nil,
					},
				},
			},
		},
	}

	group, sparams, err := mani.Spec.Group.FromCRD()
	require.NoError(t, err)

	cdep := &ClusterDeployment{
		Lid:   testutil.LeaseID(t),
		Group: &group,
		Sparams: crd.ClusterSettings{
			SchedulerParams: sparams,
		},
	}

	workload, err := NewWorkloadBuilder(myLog, mySettings, cdep, mani, 0)
	require.NoError(t, err)

	serviceBuilder, err := BuildService(workload, false, nil)
	require.NoError(t, err)

	// Should have work to do
	require.True(t, serviceBuilder.Any())

	result, err := serviceBuilder.Create()
	require.NoError(t, err)
	require.Equal(t, result.Spec.Type, corev1.ServiceTypeClusterIP)
	ports := result.Spec.Ports
	require.Equal(t, ports[0].Port, int32(2001))
	require.Equal(t, ports[0].TargetPort, intstr.FromInt(2000))
	require.Equal(t, ports[0].Name, "1-2001")
}

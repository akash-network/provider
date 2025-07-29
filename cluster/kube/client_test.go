package kube

import (
	"context"
	"testing"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/akash-network/node/sdl"
	"github.com/akash-network/node/testutil"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/akash-network/provider/cluster/kube/builder"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	afake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
)

const testKubeClientNs = "nstest1111"

func clientForTest(t *testing.T, kobjs []runtime.Object, aobjs []runtime.Object) Client {
	myLog := testutil.Logger(t)

	kc := fake.NewSimpleClientset(kobjs...)
	ac := afake.NewSimpleClientset(aobjs...)

	result := &client{
		kc:                kc,
		ac:                ac,
		ns:                testKubeClientNs,
		log:               myLog.With("mode", "test-kube-provider-client"),
		kubeContentConfig: &rest.Config{},
	}

	return result
}

func fakeProviderHost(hostname string, leaseID mtypes.LeaseID, serviceName string, externalPort uint32) runtime.Object {
	labels := make(map[string]string)
	builder.AppendLeaseLabels(leaseID, labels)
	return &crd.ProviderHost{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:                       hostname,
			GenerateName:               "",
			Namespace:                  testKubeClientNs,
			UID:                        "",
			ResourceVersion:            "",
			Generation:                 0,
			CreationTimestamp:          metav1.Time{},
			DeletionTimestamp:          nil,
			DeletionGracePeriodSeconds: nil,
			Labels:                     labels,
			Annotations:                nil,
			OwnerReferences:            nil,
			Finalizers:                 nil,
			// ClusterName:                "", // fixme @troian to check why it is not available in a new repo
			ManagedFields: nil,
		},
		Spec: crd.ProviderHostSpec{
			Owner:        leaseID.Owner,
			Provider:     leaseID.Provider,
			Hostname:     hostname,
			Dseq:         leaseID.DSeq,
			Gseq:         leaseID.GSeq,
			Oseq:         leaseID.OSeq,
			ServiceName:  serviceName,
			ExternalPort: externalPort,
		},
	}
}

func TestNewClientWithBogusIngressDomain(t *testing.T) {
	settings := builder.Settings{
		DeploymentIngressStaticHosts: true,
		DeploymentIngressDomain:      "*.foo.bar.com",
	}
	ctx := context.WithValue(context.Background(), builder.SettingsKey, settings)

	client := clientForTest(t, []runtime.Object{}, []runtime.Object{})
	require.NotNil(t, client)

	result, err := client.LeaseStatus(ctx, testutil.LeaseID(t))
	require.Error(t, err)
	require.ErrorIs(t, err, builder.ErrSettingsValidation)
	require.Nil(t, result)

	settings = builder.Settings{
		DeploymentIngressStaticHosts: true,
		DeploymentIngressDomain:      "foo.bar.com-",
	}
	ctx = context.WithValue(context.Background(), builder.SettingsKey, settings)
	result, err = client.LeaseStatus(ctx, testutil.LeaseID(t))
	require.Error(t, err)
	require.ErrorIs(t, err, builder.ErrSettingsValidation)
	require.Nil(t, result)

	settings = builder.Settings{
		DeploymentIngressStaticHosts: true,
		DeploymentIngressDomain:      "foo.ba!!!r.com",
	}
	ctx = context.WithValue(context.Background(), builder.SettingsKey, settings)
	result, err = client.LeaseStatus(ctx, testutil.LeaseID(t))
	require.Error(t, err)
	require.ErrorIs(t, err, builder.ErrSettingsValidation)
	require.Nil(t, result)
}

func TestNewClientWithEmptyIngressDomain(t *testing.T) {
	settings := builder.Settings{
		DeploymentIngressStaticHosts: true,
		DeploymentIngressDomain:      "",
	}

	client := clientForTest(t, []runtime.Object{}, []runtime.Object{})

	ctx := context.WithValue(context.Background(), builder.SettingsKey, settings)
	result, err := client.LeaseStatus(ctx, testutil.LeaseID(t))
	require.Error(t, err)
	require.ErrorIs(t, err, builder.ErrSettingsValidation)
	require.Nil(t, result)
}

func TestLeaseStatusWithNoDeployments(t *testing.T) {
	lid := testutil.LeaseID(t)

	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
	clientInterface := clientForTest(t, []runtime.Object{lns}, []runtime.Object{})

	ctx := context.WithValue(context.Background(), builder.SettingsKey, builder.Settings{
		ClusterPublicHostname: "meow.com",
	})

	status, err := clientInterface.LeaseStatus(ctx, lid)
	require.Equal(t, kubeclienterrors.ErrNoDeploymentForLease, err)
	require.Nil(t, status)
}

func TestLeaseStatusWithNoIngressNoService(t *testing.T) {
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "A",
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 10,
			Replicas:          10,
		},
	}

	clientInterface := clientForTest(t, []runtime.Object{lns, depl}, []runtime.Object{})

	ctx := context.WithValue(context.Background(), builder.SettingsKey, builder.Settings{
		ClusterPublicHostname: "meow.com",
	})
	status, err := clientInterface.LeaseStatus(ctx, lid)
	require.NoError(t, err)
	require.NotNil(t, status)
}

func TestLeaseStatusWithIngressOnly(t *testing.T) {
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	fhost := fakeProviderHost("mytesthost.dev", lid, "myingress", 1337)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	depl1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myingress",
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 10,
			Replicas:          10,
		},
	}

	depl2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "noingress",
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
			Replicas:          1,
		},
	}

	clientInterface := clientForTest(t, []runtime.Object{lns, depl1, depl2}, []runtime.Object{fhost})

	ctx := context.WithValue(context.Background(), builder.SettingsKey, builder.Settings{
		ClusterPublicHostname: "meow.com",
	})

	status, err := clientInterface.LeaseStatus(ctx, lid)
	require.NoError(t, err)
	require.NotNil(t, status)
	require.Len(t, status, 2)

	myIngressService, found := status["myingress"]
	require.True(t, found)

	require.Equal(t, myIngressService.Name, "myingress")
	require.Len(t, myIngressService.URIs, 1)
	require.Equal(t, myIngressService.URIs[0], "mytesthost.dev")

	noIngressService, found := status["noingress"]
	require.True(t, found)

	require.Equal(t, noIngressService.Name, "noingress")
	require.Len(t, noIngressService.URIs, 0)

	// Test fordwared ports - there should not be any
	fps, err := clientInterface.ForwardedPortStatus(ctx, lid)
	require.NoError(t, err)
	require.NotNil(t, fps)
	require.Len(t, fps, 0)
}

func TestLeaseStatusWithForwardedPortOnly(t *testing.T) {
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	const serviceName = "myservice"
	const expectedExternalPort = 13211

	depl1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 10,
			Replicas:          10,
		},
	}

	depl2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "noservice",
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
			Replicas:          1,
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName + builder.SuffixForNodePortServiceName,
			Namespace: ns,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					NodePort: expectedExternalPort,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}

	clientInterface := clientForTest(t, []runtime.Object{lns, svc, depl1, depl2}, []runtime.Object{})

	ctx := context.WithValue(context.Background(), builder.SettingsKey, builder.Settings{
		ClusterPublicHostname: "meow.com",
	})
	status, err := clientInterface.LeaseStatus(ctx, lid)
	require.NoError(t, err)
	require.NotNil(t, status)

	require.Len(t, status, 2)
	for _, service := range status {
		require.Len(t, service.URIs, 0) // No ingresses, so there should be no URIs
	}

	// Test forwarded ports
	fps, err := clientInterface.ForwardedPortStatus(ctx, lid)
	require.NoError(t, err)
	require.NotNil(t, fps)

	require.Len(t, fps, 1)

	ports, exists := fps[serviceName]
	require.True(t, exists)
	require.Len(t, ports, 1)
	require.Equal(t, int(ports[0].ExternalPort), expectedExternalPort)
}

func TestServiceStatusNoLease(t *testing.T) {
	const serviceName = "foobar"
	lid := testutil.LeaseID(t)

	clientInterface := clientForTest(t, nil, nil)

	status, err := clientInterface.ServiceStatus(context.Background(), lid, serviceName)
	require.ErrorIs(t, err, kubeclienterrors.ErrLeaseNotFound)
	require.Nil(t, status)
}

func TestServiceStatusNoDeployment(t *testing.T) {
	const serviceName = "foobar"
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: ns,
		},
		Spec: corev1.ServiceSpec{},
	}

	mani := &crd.Manifest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      builder.LidNS(lid),
			Namespace: testKubeClientNs,
		},
	}

	clientInterface := clientForTest(t, []runtime.Object{lns, svc}, []runtime.Object{mani})

	status, err := clientInterface.ServiceStatus(context.Background(), lid, serviceName)
	require.ErrorIs(t, err, kubeclienterrors.ErrNoServiceForLease)
	require.Nil(t, status)
}

func TestServiceStatusNoServiceWithName(t *testing.T) {
	const serviceName = "foobar"
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 10,
			Replicas:          10,
		},
	}

	mg := &manifest.Group{
		Name:     "somename",
		Services: nil,
	}

	m, err := crd.NewManifest(testKubeClientNs, lid, mg, crd.ClusterSettings{SchedulerParams: nil})
	require.NoError(t, err)

	clientInterface := clientForTest(t, []runtime.Object{lns, depl}, []runtime.Object{m})

	status, err := clientInterface.ServiceStatus(context.Background(), lid, serviceName)
	require.ErrorIs(t, err, kubeclienterrors.ErrNoServiceForLease)
	require.Nil(t, status)
}

func TestServiceStatusNoCRDManifest(t *testing.T) {
	const serviceName = "foobar"
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 10,
			Replicas:          10,
		},
	}

	mg := &manifest.Group{
		Name:     "somename",
		Services: nil,
	}

	m, err := crd.NewManifest(testKubeClientNs+"a", lid, mg, crd.ClusterSettings{SchedulerParams: nil})
	require.NoError(t, err)

	clientInterface := clientForTest(t, []runtime.Object{lns, depl}, []runtime.Object{m})

	status, err := clientInterface.ServiceStatus(context.Background(), lid, serviceName)
	require.Error(t, err)
	require.EqualError(t, err, kubeclienterrors.ErrNoManifestForLease.Error())
	require.Nil(t, status)
}

func TestServiceStatusWithIngress(t *testing.T) {
	const serviceName = "foobar"
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 10,
			Replicas:          10,
		},
	}

	services := make([]manifest.Service, 2)
	services[0] = manifest.Service{
		Name:      "someService",
		Image:     "best/image",
		Command:   nil,
		Args:      nil,
		Env:       nil,
		Resources: types.Resources{},
		Count:     1,
		Expose: []manifest.ServiceExpose{
			{
				Port:         9000,
				ExternalPort: 9000,
				Proto:        "TCP",
				Service:      "echo",
				Global:       false,
				Hosts:        nil,
			},
		},
	}
	services[1] = manifest.Service{
		Name:      serviceName,
		Image:     "best/image",
		Command:   nil,
		Args:      nil,
		Env:       nil,
		Resources: types.Resources{},
		Count:     1,
		Expose: []manifest.ServiceExpose{
			{
				Port:         9000,
				ExternalPort: 80,
				Proto:        "TCP",
				Service:      "echo",
				Global:       true,
				Hosts:        []string{"atest.localhost"},
			},
		},
	}

	mg := &manifest.Group{
		Name:     "my-awesome-group",
		Services: services,
	}

	cparams := crd.ClusterSettings{
		SchedulerParams: make([]*crd.SchedulerParams, len(mg.Services)),
	}

	m, err := crd.NewManifest(testKubeClientNs, lid, mg, cparams)
	require.NoError(t, err)

	fhost := fakeProviderHost("abcd.com", lid, "echo", 9000)

	clientInterface := clientForTest(t, []runtime.Object{lns, depl}, []runtime.Object{m, fhost})

	status, err := clientInterface.ServiceStatus(context.Background(), lid, serviceName)
	require.NoError(t, err)
	require.NotNil(t, status)

	require.Equal(t, []string{"abcd.com"}, status.URIs)
}

func TestServiceStatusWithNoManifest(t *testing.T) {
	const serviceName = "foobar"
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aname4",
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 10,
			Replicas:          10,
		},
	}

	services := make(manifest.Services, 2)
	services[0] = manifest.Service{
		Name:      "someService",
		Image:     "best/image",
		Command:   nil,
		Args:      nil,
		Env:       nil,
		Resources: types.Resources{},
		Count:     1,
		Expose: []manifest.ServiceExpose{
			{
				Port:         9000,
				ExternalPort: 9000,
				Proto:        "TCP",
				Service:      "echo",
				Global:       false,
				Hosts:        nil,
			},
		},
	}
	services[1] = manifest.Service{
		Name:      serviceName,
		Image:     "best/image",
		Command:   nil,
		Args:      nil,
		Env:       nil,
		Resources: types.Resources{},
		Count:     1,
		Expose: []manifest.ServiceExpose{
			{
				Port:         9000,
				ExternalPort: 80,
				Proto:        "TCP",
				Service:      "echo",
				Global:       true,
				Hosts:        []string{"atest.localhost"},
			},
		},
	}

	clientInterface := clientForTest(t, []runtime.Object{lns, depl}, nil)

	status, err := clientInterface.ServiceStatus(context.Background(), lid, serviceName)
	require.Error(t, err)
	require.Nil(t, status)
	require.EqualError(t, err, kubeclienterrors.ErrNoManifestForLease.Error())
}

func TestServiceStatusWithoutIngress(t *testing.T) {
	const serviceName = "foobar"
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: ns,
		},
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 10,
			Replicas:          10,
		},
	}

	services := make(manifest.Services, 2)
	services[0] = manifest.Service{
		Name:      "someService",
		Image:     "best/image",
		Command:   nil,
		Args:      nil,
		Env:       nil,
		Resources: types.Resources{},
		Count:     1,
		Expose: []manifest.ServiceExpose{
			{
				Port:         9000,
				ExternalPort: 9000,
				Proto:        "TCP",
				Service:      "echo",
				Global:       false,
				Hosts:        nil,
			},
		},
	}
	services[1] = manifest.Service{
		Name:      serviceName,
		Image:     "best/image",
		Command:   nil,
		Args:      nil,
		Env:       nil,
		Resources: types.Resources{},
		Count:     1,
		Expose: []manifest.ServiceExpose{
			{
				Port:         9000,
				ExternalPort: 80,
				Proto:        "TCP",
				Service:      "echo",
				Global:       false,
				Hosts:        []string{"atest.localhost"},
			},
		},
	}
	mg := &manifest.Group{
		Name:     "my-awesome-group",
		Services: services,
	}

	cparams := crd.ClusterSettings{
		SchedulerParams: make([]*crd.SchedulerParams, len(mg.Services)),
	}

	m, err := crd.NewManifest(testKubeClientNs, lid, mg, cparams)
	require.NoError(t, err)
	// akashMock := akashclient_fake.NewSimpleClientset(m)

	clientInterface := clientForTest(t, []runtime.Object{lns, depl}, []runtime.Object{m})

	status, err := clientInterface.ServiceStatus(context.Background(), lid, serviceName)
	require.NoError(t, err)
	require.NotNil(t, status)
	require.Len(t, status.URIs, 0)
}

func TestServiceStatusStatefulSetDetection(t *testing.T) {
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	lns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}

	testCases := []struct {
		name             string
		serviceName      string
		services         manifest.Services
		expectedWorkload string
	}{
		{
			name:        "PersistentStorage_UsesStatefulSet",
			serviceName: "postgres",
			services: manifest.Services{
				{
					Name:  "postgres",
					Image: "postgres:latest",
					Resources: types.Resources{
						Storage: []types.Storage{
							{
								Name:     "data",
								Quantity: types.NewResourceValue(10737418240), // 10Gi
								Attributes: types.Attributes{
									{
										Key:   sdl.StorageAttributePersistent,
										Value: "true",
									},
								},
							},
						},
					},
					Count: 1,
				},
			},
			expectedWorkload: "StatefulSet",
		},
		{
			name:        "NonPersistentStorage_UsesDeployment",
			serviceName: "web",
			services: manifest.Services{
				{
					Name:  "web",
					Image: "nginx:latest",
					Resources: types.Resources{
						Storage: []types.Storage{
							{
								Name:     "tmp",
								Quantity: types.NewResourceValue(1073741824), // 1Gi
								Attributes: types.Attributes{
									{
										Key:   sdl.StorageAttributePersistent,
										Value: "false",
									},
								},
							},
						},
					},
					Count: 2,
				},
			},
			expectedWorkload: "Deployment",
		},
		{
			name:        "NoStorage_UsesDeployment",
			serviceName: "api",
			services: manifest.Services{
				{
					Name:      "api",
					Image:     "myapp:latest",
					Resources: types.Resources{},
					Count:     3,
				},
			},
			expectedWorkload: "Deployment",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create the manifest
			mg := &manifest.Group{
				Name:     "test-group",
				Services: tc.services,
			}

			cparams := crd.ClusterSettings{
				SchedulerParams: make([]*crd.SchedulerParams, len(mg.Services)),
			}

			m, err := crd.NewManifest(testKubeClientNs, lid, mg, cparams)
			require.NoError(t, err)

			// Create the deployment or statefulset
			var kobjs []runtime.Object
			kobjs = append(kobjs, lns)

			if tc.expectedWorkload == "StatefulSet" {
				ss := &appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tc.serviceName,
						Namespace: ns,
					},
					Spec: appsv1.StatefulSetSpec{},
					Status: appsv1.StatefulSetStatus{
						AvailableReplicas: 1,
						Replicas:          1,
					},
				}
				kobjs = append(kobjs, ss)
			} else {
				depl := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tc.serviceName,
						Namespace: ns,
					},
					Spec: appsv1.DeploymentSpec{},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: int32(tc.services[0].Count),
						Replicas:          int32(tc.services[0].Count),
					},
				}
				kobjs = append(kobjs, depl)
			}

			clientInterface := clientForTest(t, kobjs, []runtime.Object{m})

			// Test ServiceStatus
			status, err := clientInterface.ServiceStatus(context.Background(), lid, tc.serviceName)
			require.NoError(t, err)
			require.NotNil(t, status)
			require.Equal(t, tc.serviceName, status.Name)

			// Verify we can find the correct workload type
			if tc.expectedWorkload == "StatefulSet" {
				require.Equal(t, uint32(1), status.Available)
				require.Equal(t, uint32(1), status.Total)
			} else {
				require.Equal(t, tc.services[0].Count, status.Available)
				require.Equal(t, tc.services[0].Count, status.Total)
			}
		})
	}
}

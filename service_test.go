package provider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	manifesttypes "pkg.akt.dev/go/manifest/v2beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	apclient "pkg.akt.dev/go/provider/client"
	providerv1 "pkg.akt.dev/go/provider/v1"

	clustermocks "github.com/akash-network/provider/mocks/cluster"
)

type fakeBidEngineService struct {
	status   *apclient.BidEngineStatus
	statusV1 *providerv1.BidEngineStatus
	done     chan struct{}
}

func (s fakeBidEngineService) Status(context.Context) (*apclient.BidEngineStatus, error) {
	return s.status, nil
}

func (s fakeBidEngineService) StatusV1(context.Context) (*providerv1.BidEngineStatus, error) {
	return s.statusV1, nil
}

func (s fakeBidEngineService) Close() error {
	return nil
}

func (s fakeBidEngineService) Done() <-chan struct{} {
	return s.done
}

type fakeManifestService struct {
	status   *apclient.ManifestStatus
	statusV1 *providerv1.ManifestStatus
	done     chan struct{}
}

func (s fakeManifestService) Status(context.Context) (*apclient.ManifestStatus, error) {
	return s.status, nil
}

func (s fakeManifestService) StatusV1(context.Context) (*providerv1.ManifestStatus, error) {
	return s.statusV1, nil
}

func (s fakeManifestService) Submit(context.Context, dtypes.DeploymentID, manifesttypes.Manifest) error {
	return nil
}

func (s fakeManifestService) IsActive(context.Context, dtypes.DeploymentID) (bool, error) {
	return false, nil
}

func (s fakeManifestService) Done() <-chan struct{} {
	return s.done
}

func (s fakeManifestService) Close() error {
	return nil
}

func TestServiceStatusIncludesReclamationWindow(t *testing.T) {
	reclamationWindow := 24 * time.Hour
	clusterSvc := clustermocks.NewService(t)
	clusterSvc.EXPECT().
		Status(mock.Anything).
		Return(&apclient.ClusterStatus{}, nil)
	clusterSvc.EXPECT().
		StatusV1(mock.Anything).
		Return(&providerv1.ClusterStatus{}, nil)

	svc := &service{
		config: Config{
			ClusterPublicHostname: "provider.example",
			ReclamationWindow:     &reclamationWindow,
		},
		cluster: clusterSvc,
		bidengine: fakeBidEngineService{
			status:   &apclient.BidEngineStatus{},
			statusV1: &providerv1.BidEngineStatus{},
			done:     make(chan struct{}),
		},
		manifest: fakeManifestService{
			status:   &apclient.ManifestStatus{},
			statusV1: &providerv1.ManifestStatus{},
			done:     make(chan struct{}),
		},
	}

	status, err := svc.Status(context.Background())
	require.NoError(t, err)
	require.NotNil(t, status.ReclamationWindow)
	require.Equal(t, reclamationWindow, *status.ReclamationWindow)

	statusV1, err := svc.StatusV1(context.Background())
	require.NoError(t, err)
	require.NotNil(t, statusV1.GetReclamationWindow())
	require.Equal(t, reclamationWindow, *statusV1.GetReclamationWindow())
}

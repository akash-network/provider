package provider

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/log"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	audittypes "pkg.akt.dev/go/node/audit/v1"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	providerv1 "pkg.akt.dev/go/provider/v1"

	"github.com/akash-network/provider/bidengine"
	mockcluster "github.com/akash-network/provider/mocks/cluster"
	mockreservation "github.com/akash-network/provider/mocks/cluster/types"
	"github.com/akash-network/provider/session"
)

type stubSession struct {
	session.Session
	provider *ptypes.Provider
}

func (s *stubSession) Provider() *ptypes.Provider { return s.provider }
func (s *stubSession) Log() log.Logger             { return log.NewNopLogger() }

type stubBidEngine struct {
	bidengine.Service
	attrSvc bidengine.ProviderAttrSignatureService
}

func (s *stubBidEngine) ProviderAttrService() bidengine.ProviderAttrSignatureService {
	return s.attrSvc
}

type stubAttrService struct{}

func (stubAttrService) GetAttributes() (attrtypes.Attributes, error)                              { return nil, nil }
func (stubAttrService) GetAuditorAttributeSignatures(_ string) (audittypes.AuditedProviders, error) { return nil, nil }

type stubPricing struct {
	price sdk.DecCoin
	err   error
}

func (s *stubPricing) CalculatePrice(_ context.Context, _ bidengine.Request) (sdk.DecCoin, error) {
	return s.price, s.err
}

func makeTestGroupSpec() *dtypes.GroupSpec {
	cpu := rtypes.CPU{}
	cpu.Units = rtypes.NewResourceValue(uint64(dtypes.GetValidationConfig().Unit.Min.CPU))
	gpu := rtypes.GPU{}
	gpu.Units = rtypes.NewResourceValue(uint64(dtypes.GetValidationConfig().Unit.Min.GPU))
	memory := rtypes.Memory{}
	memory.Quantity = rtypes.NewResourceValue(dtypes.GetValidationConfig().Unit.Min.Memory)
	storage := rtypes.Volumes{
		rtypes.Storage{Quantity: rtypes.NewResourceValue(dtypes.GetValidationConfig().Unit.Min.Storage)},
	}

	gs := dtypes.GroupSpec{
		Name: "test",
		Resources: dtypes.ResourceUnits{
			{
				Resources: rtypes.Resources{
					ID:      1,
					CPU:     &cpu,
					GPU:     &gpu,
					Memory:  &memory,
					Storage: storage,
				},
				Count: 1,
				Price: sdk.NewInt64DecCoin("uakt", 1),
			},
		},
	}
	return &gs
}

func makeTestService(t *testing.T, pricing bidengine.BidPricingStrategy) (*service, *mockcluster.Service) {
	t.Helper()

	clusterSvc := mockcluster.NewService(t)

	svc := &service{
		session: &stubSession{
			provider: &ptypes.Provider{
				Owner:      "akash1testprovider",
				Attributes: attrtypes.Attributes{},
			},
		},
		bidengine: &stubBidEngine{attrSvc: &stubAttrService{}},
		cluster:   clusterSvc,
		config: Config{
			MaxGroupVolumes:    1,
			BidPricingStrategy: pricing,
		},
	}

	return svc, clusterSvc
}

func TestBidScreening_nil_group_spec(t *testing.T) {
	svc, _ := makeTestService(t, &stubPricing{})

	resp, err := svc.BidScreening(context.Background(), &providerv1.BidScreeningRequest{})
	require.NoError(t, err)
	require.False(t, resp.Passed)
	require.Contains(t, resp.Reasons, "missing group spec")
}

func TestBidScreening_nil_owner_no_panic(t *testing.T) {
	svc, clusterSvc := makeTestService(t, &stubPricing{
		price: sdk.NewInt64DecCoin("uakt", 100),
	})

	gspec := makeTestGroupSpec()
	reservation := mockreservation.NewReservation(t)
	reservation.EXPECT().GetAllocatedResources().Return(gspec.Resources)

	clusterSvc.EXPECT().HostnameService().Return(nil)
	clusterSvc.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return(reservation, nil)

	resp, err := svc.BidScreening(context.Background(), &providerv1.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.NoError(t, err)
	require.True(t, resp.Passed)
}

func TestBidScreening_nil_owner_with_hostnames(t *testing.T) {
	svc, _ := makeTestService(t, &stubPricing{})

	gspec := makeTestGroupSpec()

	resp, err := svc.BidScreening(context.Background(), &providerv1.BidScreeningRequest{
		GroupSpec: gspec,
		Hostnames: []string{"example.com"},
	})
	require.NoError(t, err)
	require.False(t, resp.Passed)
	require.Contains(t, resp.Reasons, "missing owner in context; required for hostname availability check")
}

func TestBidScreening_with_owner(t *testing.T) {
	svc, clusterSvc := makeTestService(t, &stubPricing{
		price: sdk.NewInt64DecCoin("uakt", 100),
	})

	gspec := makeTestGroupSpec()
	reservation := mockreservation.NewReservation(t)
	reservation.EXPECT().GetAllocatedResources().Return(gspec.Resources)

	clusterSvc.EXPECT().HostnameService().Return(nil)
	clusterSvc.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return(reservation, nil)

	owner := sdk.AccAddress("testowner12345678901")
	ctx := ContextWithOwner(context.Background(), owner)

	resp, err := svc.BidScreening(ctx, &providerv1.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.NoError(t, err)
	require.True(t, resp.Passed)
	require.NotNil(t, resp.Price)
}

func TestBidScreening_cluster_reserve_fails(t *testing.T) {
	svc, clusterSvc := makeTestService(t, &stubPricing{})

	gspec := makeTestGroupSpec()

	clusterSvc.EXPECT().HostnameService().Return(nil)
	clusterSvc.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("insufficient capacity"))

	resp, err := svc.BidScreening(context.Background(), &providerv1.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.NoError(t, err)
	require.False(t, resp.Passed)
	require.Len(t, resp.Reasons, 1)
	require.Contains(t, resp.Reasons[0], "cluster capacity")
}

func TestBidScreening_pricing_error(t *testing.T) {
	svc, clusterSvc := makeTestService(t, &stubPricing{
		err: errors.New("pricing unavailable"),
	})

	gspec := makeTestGroupSpec()
	reservation := mockreservation.NewReservation(t)
	reservation.EXPECT().GetAllocatedResources().Return(gspec.Resources)

	clusterSvc.EXPECT().HostnameService().Return(nil)
	clusterSvc.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return(reservation, nil)

	_, err := svc.BidScreening(context.Background(), &providerv1.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pricing unavailable")
}

func TestOwnerFromCtx_nil_when_not_set(t *testing.T) {
	owner := ownerFromCtx(context.Background())
	require.Nil(t, owner)
}

func TestOwnerFromCtx_roundtrip(t *testing.T) {
	addr := sdk.AccAddress("testowner12345678901")
	ctx := ContextWithOwner(context.Background(), addr)

	got := ownerFromCtx(ctx)
	require.NotNil(t, got)
	require.Equal(t, addr.String(), got.String())
}

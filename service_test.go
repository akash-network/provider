package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"pkg.akt.dev/go/sdkutil"

	sdk "github.com/cosmos/cosmos-sdk/types"

	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	apclient "pkg.akt.dev/go/provider/client"
	provider "pkg.akt.dev/go/provider/v1"

	"github.com/akash-network/provider/bidengine"
)

// fakeBidEngine is a minimal mock of bidengine.Service for testing provider-level ScreenBid.
type fakeBidEngine struct {
	result *bidengine.ScreenBidResult
	err    error
}

func (f *fakeBidEngine) ScreenBid(_ context.Context, _ *dvbeta.GroupSpec) (*bidengine.ScreenBidResult, error) {
	return f.result, f.err
}

func (f *fakeBidEngine) Status(_ context.Context) (*apclient.BidEngineStatus, error) {
	return nil, nil
}

func (f *fakeBidEngine) StatusV1(_ context.Context) (*provider.BidEngineStatus, error) {
	return nil, nil
}

func (f *fakeBidEngine) Close() error { return nil }

func (f *fakeBidEngine) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func validTestGroupSpec() *dvbeta.GroupSpec {
	return &dvbeta.GroupSpec{
		Name: "test-group",
		Resources: dvbeta.ResourceUnits{
			{
				Resources: rtypes.Resources{
					ID: 1,
					CPU: &rtypes.CPU{
						Units: rtypes.NewResourceValue(100),
					},
					Memory: &rtypes.Memory{
						Quantity: rtypes.NewResourceValue(1024 * 1024 * 1024),
					},
					GPU: &rtypes.GPU{
						Units: rtypes.NewResourceValue(0),
					},
					Storage: rtypes.Volumes{
						rtypes.Storage{
							Quantity: rtypes.NewResourceValue(1024 * 1024 * 1024),
						},
					},
				},
				Count: 1,
				Price: sdk.NewInt64DecCoin(sdkutil.DenomUact, 10),
			},
		},
	}
}

func Test_ServiceScreenBid_Passed(t *testing.T) {
	gspec := validTestGroupSpec()
	expectedPrice := sdk.NewInt64DecCoin(sdkutil.DenomUact, 42)

	fbe := &fakeBidEngine{
		result: &bidengine.ScreenBidResult{
			Passed:             true,
			AllocatedResources: gspec.Resources,
			Price:              expectedPrice,
		},
	}

	svc := &service{bidengine: fbe}

	resp, err := svc.ScreenBid(context.Background(), &provider.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Passed)
	require.Empty(t, resp.Reasons)
	require.NotNil(t, resp.Price)
	require.Equal(t, expectedPrice.Denom, resp.Price.Denom)
	require.True(t, expectedPrice.Amount.Equal(resp.Price.Amount))
	require.Len(t, resp.ResourceOffers, 1)
}

func Test_ServiceScreenBid_Failed(t *testing.T) {
	gspec := validTestGroupSpec()
	reasons := []string{"incompatible provider attributes", "insufficient capacity: not enough GPUs"}

	fbe := &fakeBidEngine{
		result: &bidengine.ScreenBidResult{
			Passed:  false,
			Reasons: reasons,
		},
	}

	svc := &service{bidengine: fbe}

	resp, err := svc.ScreenBid(context.Background(), &provider.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.Passed)
	require.Equal(t, reasons, resp.Reasons)
	require.Empty(t, resp.ResourceOffers)
	require.Nil(t, resp.Price)
}

func Test_ServiceScreenBid_Error(t *testing.T) {
	gspec := validTestGroupSpec()
	expectedErr := errors.New("bidengine internal error")

	fbe := &fakeBidEngine{err: expectedErr}

	svc := &service{bidengine: fbe}

	resp, err := svc.ScreenBid(context.Background(), &provider.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, resp)
}

func Test_ServiceScreenBid_MultiService(t *testing.T) {
	gspec := validTestGroupSpec()
	secondUnit := dvbeta.ResourceUnit{
		Resources: rtypes.Resources{
			ID: 2,
			CPU: &rtypes.CPU{
				Units: rtypes.NewResourceValue(200),
			},
			Memory: &rtypes.Memory{
				Quantity: rtypes.NewResourceValue(2 * 1024 * 1024 * 1024),
			},
			GPU: &rtypes.GPU{
				Units: rtypes.NewResourceValue(0),
			},
			Storage: rtypes.Volumes{
				rtypes.Storage{
					Quantity: rtypes.NewResourceValue(1024 * 1024 * 1024),
				},
			},
		},
		Count: 1,
		Price: sdk.NewInt64DecCoin(sdkutil.DenomUact, 20),
	}
	multiResources := append(gspec.Resources, secondUnit) //nolint:gocritic
	expectedPrice := sdk.NewInt64DecCoin(sdkutil.DenomUact, 42)

	fbe := &fakeBidEngine{
		result: &bidengine.ScreenBidResult{
			Passed:             true,
			AllocatedResources: multiResources,
			Price:              expectedPrice,
		},
	}

	svc := &service{bidengine: fbe}

	resp, err := svc.ScreenBid(context.Background(), &provider.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Passed)
	require.Len(t, resp.ResourceOffers, 2)
	require.NotNil(t, resp.Price)
	require.Empty(t, resp.Reasons)
}

func Test_ServiceScreenBid_PassedWithEmptyResources(t *testing.T) {
	gspec := validTestGroupSpec()
	expectedPrice := sdk.NewInt64DecCoin(sdkutil.DenomUact, 10)

	fbe := &fakeBidEngine{
		result: &bidengine.ScreenBidResult{
			Passed:             true,
			AllocatedResources: nil, // empty allocated resources
			Price:              expectedPrice,
		},
	}

	svc := &service{bidengine: fbe}

	resp, err := svc.ScreenBid(context.Background(), &provider.BidScreeningRequest{
		GroupSpec: gspec,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Passed)
	require.Empty(t, resp.ResourceOffers)
	require.NotNil(t, resp.Price)
}

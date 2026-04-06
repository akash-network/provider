package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"pkg.akt.dev/go/sdkutil"

	sdk "github.com/cosmos/cosmos-sdk/types"

	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	providerv1 "pkg.akt.dev/go/provider/v1"

	pmock "github.com/akash-network/provider/mocks/client"
	ctmocks "github.com/akash-network/provider/mocks/cluster/types"
)

func testGroupSpec() *dvbeta.GroupSpec {
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

func Test_BidScreening_DelegatesToClient(t *testing.T) {
	pclient := &pmock.Client{}

	gspec := testGroupSpec()
	price := sdk.NewInt64DecCoin(sdkutil.DenomUact, 42)
	offers := mvbeta.ResourceOfferFromRU(gspec.Resources)

	req := &providerv1.BidScreeningRequest{GroupSpec: gspec}
	expectedResp := &providerv1.BidScreeningResponse{
		Passed: true,
		Price:  &price,
	}
	if len(offers) > 0 {
		expectedResp.ResourceOffer = &offers[0]
	}

	pclient.On("ScreenBid", mock.Anything, req).Return(expectedResp, nil)

	handler := &grpcProviderV1{
		ctx:    context.Background(),
		client: pclient,
	}

	resp, err := handler.BidScreening(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, expectedResp, resp)

	pclient.AssertCalled(t, "ScreenBid", mock.Anything, req)
}

func Test_BidScreening_PropagatesError(t *testing.T) {
	pclient := &pmock.Client{}

	gspec := testGroupSpec()
	req := &providerv1.BidScreeningRequest{GroupSpec: gspec}
	expectedErr := errors.New("internal server error")

	pclient.On("ScreenBid", mock.Anything, req).Return(nil, expectedErr)

	handler := &grpcProviderV1{
		ctx:    context.Background(),
		client: pclient,
	}

	resp, err := handler.BidScreening(context.Background(), req)
	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, resp)

	pclient.AssertCalled(t, "ScreenBid", mock.Anything, req)
}

func Test_BidScreening_FailedScreening(t *testing.T) {
	pclient := &pmock.Client{}

	gspec := testGroupSpec()
	req := &providerv1.BidScreeningRequest{GroupSpec: gspec}
	reasons := []string{"incompatible provider attributes"}
	expectedResp := &providerv1.BidScreeningResponse{
		Passed:  false,
		Reasons: reasons,
	}

	pclient.On("ScreenBid", mock.Anything, req).Return(expectedResp, nil)

	handler := &grpcProviderV1{
		ctx:    context.Background(),
		client: pclient,
	}

	resp, err := handler.BidScreening(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.Passed)
	require.Equal(t, reasons, resp.Reasons)
	require.Nil(t, resp.ResourceOffer)
	require.Nil(t, resp.Price)

	pclient.AssertCalled(t, "ScreenBid", mock.Anything, req)
}

func Test_BidScreening_HostnameBlocked(t *testing.T) {
	pclient := &pmock.Client{}

	hostnameMock := &ctmocks.HostnameServiceClient{}
	hostnameMock.On("CanReserveHostnames", mock.Anything, mock.Anything).Return(errors.New("hostname blocked by this provider"))
	pclient.On("Hostname").Return(hostnameMock)

	req := &providerv1.BidScreeningRequest{
		GroupSpec: testGroupSpec(),
		Hostnames: []string{"blocked.example.com"},
	}

	handler := &grpcProviderV1{
		ctx:    context.Background(),
		client: pclient,
	}

	resp, err := handler.BidScreening(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.Passed)
	require.Len(t, resp.Reasons, 1)
	require.Contains(t, resp.Reasons[0], "hostname unavailable")

	pclient.AssertNotCalled(t, "ScreenBid", mock.Anything, mock.Anything)
}

func Test_BidScreening_HostnameAvailable(t *testing.T) {
	pclient := &pmock.Client{}

	hostnameMock := &ctmocks.HostnameServiceClient{}
	hostnameMock.On("CanReserveHostnames", mock.Anything, mock.Anything).Return(nil)
	pclient.On("Hostname").Return(hostnameMock)

	price := sdk.NewInt64DecCoin(sdkutil.DenomUact, 42)
	req := &providerv1.BidScreeningRequest{
		GroupSpec: testGroupSpec(),
		Hostnames: []string{"valid.example.com"},
	}
	expectedResp := &providerv1.BidScreeningResponse{
		Passed: true,
		Price:  &price,
	}

	pclient.On("ScreenBid", mock.Anything, req).Return(expectedResp, nil)

	handler := &grpcProviderV1{
		ctx:    context.Background(),
		client: pclient,
	}

	resp, err := handler.BidScreening(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, expectedResp, resp)

	pclient.AssertCalled(t, "ScreenBid", mock.Anything, req)
	hostnameMock.AssertCalled(t, "CanReserveHostnames", mock.Anything, mock.Anything)
}

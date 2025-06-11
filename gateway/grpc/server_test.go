package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/provider"
	pmocks "github.com/akash-network/provider/mocks"
)

type mockClient struct {
	*pmocks.Client
}

func (m *mockClient) Validate(ctx context.Context, addr sdk.Address, specs dtypes.GroupSpecs) (providerv1.BidPreCheckResponse, error) {
	args := m.Called(ctx, addr, specs)
	return args.Get(0).(providerv1.BidPreCheckResponse), args.Error(1)
}

func TestBidPreCheck(t *testing.T) {
	const testAddress = "akash1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqg"

	tests := []struct {
		name          string
		setupMocks    func(*mockClient)
		request       *providerv1.BidPreCheckRequest
		expectedError string
		expectedResp  *providerv1.BidPreCheckResponse
	}{
		{
			name: "empty groups",
			request: &providerv1.BidPreCheckRequest{
				Groups: []dtypes.GroupSpec{},
			},
			expectedError: "no groups provided",
		},
		{
			name: "validation error from client",
			request: &providerv1.BidPreCheckRequest{
				Groups: []dtypes.GroupSpec{
					{
						Name: "test-group",
					},
				},
			},
			setupMocks: func(mockClient *mockClient) {
				addr, _ := sdk.AccAddressFromBech32(testAddress)
				specs := dtypes.GroupSpecs{&dtypes.GroupSpec{Name: "test-group"}}
				mockClient.On("Validate", mock.Anything, addr, specs).
					Return(providerv1.BidPreCheckResponse{}, errors.New("insufficient resources"))
			},
			expectedError: "insufficient resources",
		},
		{
			name: "successful validation",
			request: &providerv1.BidPreCheckRequest{
				Groups: []dtypes.GroupSpec{
					{
						Name: "test-group",
					},
				},
			},
			setupMocks: func(mockClient *mockClient) {
				addr, _ := sdk.AccAddressFromBech32(testAddress)
				specs := dtypes.GroupSpecs{&dtypes.GroupSpec{Name: "test-group"}}
				result := providerv1.BidPreCheckResponse{
					GroupBidPreChecks: []*providerv1.GroupBidPreCheck{
						{
							Name:        "test-group",
							MinBidPrice: sdk.NewDecCoin("uakt", sdk.NewInt(100)),
							CanBid:      true,
							Reason:      "price calculated successfully",
						},
					},
					TotalPrice: sdk.NewDecCoin("uakt", sdk.NewInt(100)),
				}
				mockClient.On("Validate", mock.Anything, addr, specs).Return(result, nil)
			},
			expectedResp: &providerv1.BidPreCheckResponse{
				GroupBidPreChecks: []*providerv1.GroupBidPreCheck{
					{
						Name:        "test-group",
						MinBidPrice: sdk.NewDecCoin("uakt", sdk.NewInt(100)),
						CanBid:      true,
						Reason:      "price calculated successfully",
					},
				},
				TotalPrice: sdk.NewDecCoin("uakt", sdk.NewInt(100)),
			},
		},
		{
			name: "missing claims in context",
			request: &providerv1.BidPreCheckRequest{
				Groups: []dtypes.GroupSpec{
					{
						Name: "test-group",
					},
				},
			},
			expectedError: "no claims found in context",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := &mockClient{pmocks.NewClient(t)}
			if tc.setupMocks != nil {
				tc.setupMocks(mockClient)
			}

			s := grpcProviderV1{
				client: mockClient,
				config: &provider.Config{},
			}

			ctx := context.Background()
			if tc.name != "missing claims in context" {
				now := time.Now()
				claims := &ajwt.Claims{
					RegisteredClaims: jwt.RegisteredClaims{
						Issuer:    testAddress,
						IssuedAt:  jwt.NewNumericDate(now),
						NotBefore: jwt.NewNumericDate(now),
						ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
					},
					Version: "v1",
					Leases: ajwt.Leases{
						Access: ajwt.AccessTypeFull,
					},
				}
				ctx = ContextWithClaims(ctx, claims)
			}

			resp, err := s.BidPreCheck(ctx, tc.request)

			if tc.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Equal(t, tc.expectedResp, resp)
			}
		})
	}
}

package bidengine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	vtypes "pkg.akt.dev/go/node/verification/v1"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/session"
)

func TestVerificationPreflightRequiresQuery(t *testing.T) {
	require.False(t, verificationPreflightRequiresQuery(nil))
	require.False(t, verificationPreflightRequiresQuery(&vtypes.VerificationRequirement{}))
	require.False(t, verificationPreflightRequiresQuery(&vtypes.VerificationRequirement{
		MinTier:              vtypes.TierUnspecified,
		RequiredCapabilities: []vtypes.CapabilityFlag{vtypes.CapabilityBareMetal},
		RequiredAuditors:     []string{"akash1auditor"},
		MinAuditorCount:      1,
	}))
	require.True(t, verificationPreflightRequiresQuery(&vtypes.VerificationRequirement{
		MinTier: vtypes.TierIdentified,
	}))
}

func TestEvaluateVerificationPreflightAllowsNoopCases(t *testing.T) {
	tests := []struct {
		name         string
		moduleActive bool
		req          *vtypes.VerificationRequirement
	}{
		{
			name:         "module inactive",
			moduleActive: false,
			req: &vtypes.VerificationRequirement{
				MinTier: vtypes.TierTrusted,
			},
		},
		{
			name:         "nil requirement",
			moduleActive: true,
		},
		{
			name:         "empty requirement",
			moduleActive: true,
			req:          &vtypes.VerificationRequirement{},
		},
		{
			name:         "tier unspecified with dependent filters",
			moduleActive: true,
			req: &vtypes.VerificationRequirement{
				MinTier:              vtypes.TierUnspecified,
				RequiredCapabilities: []vtypes.CapabilityFlag{vtypes.CapabilityBareMetal},
				RequiredAuditors:     []string{"akash1auditor"},
				MinAuditorCount:      1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := evaluateVerificationPreflight(tc.moduleActive, tc.req, nil, nil)
			require.True(t, result.allow)
		})
	}
}

func TestEvaluateVerificationPreflightTier(t *testing.T) {
	tests := []struct {
		name         string
		reqTier      vtypes.VerificationTier
		attestations []vtypes.AttestationRecord
		wantAllow    bool
		wantReason   string
	}{
		{
			name:       "no attestation fails",
			reqTier:    vtypes.TierIdentified,
			wantReason: "tier",
		},
		{
			name:    "valid lower tier fails",
			reqTier: vtypes.TierVerified,
			attestations: []vtypes.AttestationRecord{
				validAttestation("akash1auditor1", vtypes.TierIdentified),
			},
			wantReason: "tier",
		},
		{
			name:    "valid equal tier allows",
			reqTier: vtypes.TierVerified,
			attestations: []vtypes.AttestationRecord{
				validAttestation("akash1auditor1", vtypes.TierVerified),
			},
			wantAllow:  true,
			wantReason: "allow",
		},
		{
			name:    "invalid attestation ignored",
			reqTier: vtypes.TierVerified,
			attestations: []vtypes.AttestationRecord{
				{
					Auditor: "akash1auditor1",
					Tier:    vtypes.TierTrusted,
					Status:  vtypes.AttestationStatusVoided,
				},
			},
			wantReason: "tier",
		},
		{
			name:    "best valid tier allows",
			reqTier: vtypes.TierEstablished,
			attestations: []vtypes.AttestationRecord{
				validAttestation("akash1auditor1", vtypes.TierIdentified),
				validAttestation("akash1auditor2", vtypes.TierEstablished),
			},
			wantAllow:  true,
			wantReason: "allow",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := evaluateVerificationPreflight(true, &vtypes.VerificationRequirement{
				MinTier: tc.reqTier,
			}, tc.attestations, nil)
			require.Equal(t, tc.wantAllow, result.allow)
			require.Equal(t, tc.wantReason, result.reason)
		})
	}
}

func TestEvaluateVerificationPreflightActiveGraceUsesPreservedTier(t *testing.T) {
	result := evaluateVerificationPreflight(true, &vtypes.VerificationRequirement{
		MinTier: vtypes.TierVerified,
	}, []vtypes.AttestationRecord{
		validAttestation("akash1auditor1", vtypes.TierTrusted),
	}, &vtypes.ProviderVerificationGraceRecord{
		PreservedTier: vtypes.TierIdentified,
		Status:        vtypes.VerificationGraceStatusActive,
	})

	require.False(t, result.allow)
	require.Equal(t, "tier", result.reason)

	result = evaluateVerificationPreflight(true, &vtypes.VerificationRequirement{
		MinTier: vtypes.TierVerified,
	}, nil, &vtypes.ProviderVerificationGraceRecord{
		PreservedTier: vtypes.TierVerified,
		Status:        vtypes.VerificationGraceStatusActive,
	})

	require.True(t, result.allow)
	require.Equal(t, "allow", result.reason)
}

func TestEvaluateVerificationPreflightCapabilities(t *testing.T) {
	result := evaluateVerificationPreflight(true, &vtypes.VerificationRequirement{
		MinTier: vtypes.TierVerified,
		RequiredCapabilities: []vtypes.CapabilityFlag{
			vtypes.CapabilityConfidentialComputing,
			vtypes.CapabilityBareMetal,
		},
	}, []vtypes.AttestationRecord{
		{
			Auditor: "akash1auditor1",
			Tier:    vtypes.TierVerified,
			Status:  vtypes.AttestationStatusValid,
			Capabilities: []vtypes.CapabilityFlag{
				vtypes.CapabilityConfidentialComputing,
			},
		},
		{
			Auditor: "akash1auditor2",
			Tier:    vtypes.TierIdentified,
			Status:  vtypes.AttestationStatusValid,
			Capabilities: []vtypes.CapabilityFlag{
				vtypes.CapabilityBareMetal,
			},
		},
	}, nil)

	require.True(t, result.allow)

	result = evaluateVerificationPreflight(true, &vtypes.VerificationRequirement{
		MinTier:              vtypes.TierVerified,
		RequiredCapabilities: []vtypes.CapabilityFlag{vtypes.CapabilityPersistentStorage},
	}, []vtypes.AttestationRecord{
		validAttestation("akash1auditor1", vtypes.TierVerified),
	}, nil)

	require.False(t, result.allow)
	require.Equal(t, "capabilities", result.reason)
}

func TestEvaluateVerificationPreflightAuditors(t *testing.T) {
	attestations := []vtypes.AttestationRecord{
		validAttestation("akash1auditor1", vtypes.TierVerified),
		validAttestation("akash1auditor2", vtypes.TierVerified),
	}

	result := evaluateVerificationPreflight(true, &vtypes.VerificationRequirement{
		MinTier:          vtypes.TierVerified,
		RequiredAuditors: []string{"akash1missing", "akash1auditor2"},
		AuditorMode:      vtypes.AuditorSelectionModeAny,
		MinAuditorCount:  2,
	}, attestations, nil)
	require.True(t, result.allow)

	result = evaluateVerificationPreflight(true, &vtypes.VerificationRequirement{
		MinTier:          vtypes.TierVerified,
		RequiredAuditors: []string{"akash1auditor1", "akash1missing"},
		AuditorMode:      vtypes.AuditorSelectionModeAll,
	}, attestations, nil)
	require.False(t, result.allow)
	require.Equal(t, "auditors", result.reason)

	result = evaluateVerificationPreflight(true, &vtypes.VerificationRequirement{
		MinTier:         vtypes.TierVerified,
		MinAuditorCount: 3,
	}, attestations, nil)
	require.False(t, result.allow)
	require.Equal(t, "auditors", result.reason)
}

func validAttestation(auditor string, tier vtypes.VerificationTier) vtypes.AttestationRecord {
	return vtypes.AttestationRecord{
		Auditor: auditor,
		Tier:    tier,
		Status:  vtypes.AttestationStatusValid,
	}
}

func TestShouldBidVerificationPreflightNoRequirementAvoidsQueries(t *testing.T) {
	var o order
	require.True(t, o.shouldBidVerificationPreflight(context.Background(), nil))
	require.True(t, o.shouldBidVerificationPreflight(context.Background(), &vtypes.VerificationRequirement{}))
	require.True(t, o.shouldBidVerificationPreflight(context.Background(), &vtypes.VerificationRequirement{
		MinTier:              vtypes.TierUnspecified,
		RequiredCapabilities: []vtypes.CapabilityFlag{vtypes.CapabilityBareMetal},
		RequiredAuditors:     []string{"akash1auditor"},
		MinAuditorCount:      1,
	}))
}

func TestShouldBidVerificationPreflightModuleInactiveSkipsLocalQueries(t *testing.T) {
	query := &verificationPreflightQueryClient{
		paramsResp: &vtypes.QueryParamsResponse{
			Params: vtypes.Params{VerificationModuleActive: false},
		},
		attestationsErr: errors.New("unexpected attestations query"),
		graceErr:        errors.New("unexpected grace query"),
	}

	o := newVerificationPreflightTestOrder(t, query)
	require.True(t, o.shouldBidVerificationPreflight(context.Background(), &vtypes.VerificationRequirement{
		MinTier: vtypes.TierTrusted,
	}))
	require.Zero(t, query.attestationsCalls)
	require.Zero(t, query.graceCalls)
}

func TestShouldBidVerificationPreflightQueryFailuresFallOpen(t *testing.T) {
	tests := []struct {
		name  string
		query *verificationPreflightQueryClient
	}{
		{
			name: "params failure",
			query: &verificationPreflightQueryClient{
				paramsErr: errors.New("params failed"),
			},
		},
		{
			name: "attestations failure",
			query: &verificationPreflightQueryClient{
				paramsResp: &vtypes.QueryParamsResponse{
					Params: vtypes.Params{VerificationModuleActive: true},
				},
				attestationsErr: errors.New("attestations failed"),
			},
		},
		{
			name: "grace failure",
			query: &verificationPreflightQueryClient{
				paramsResp: &vtypes.QueryParamsResponse{
					Params: vtypes.Params{VerificationModuleActive: true},
				},
				attestationsResp: &vtypes.QueryProviderAttestationsResponse{},
				graceErr:         errors.New("grace failed"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := newVerificationPreflightTestOrder(t, tc.query)
			require.True(t, o.shouldBidVerificationPreflight(context.Background(), &vtypes.VerificationRequirement{
				MinTier: vtypes.TierVerified,
			}))
		})
	}
}

func TestShouldBidVerificationPreflightUsesQueriedData(t *testing.T) {
	query := &verificationPreflightQueryClient{
		paramsResp: &vtypes.QueryParamsResponse{
			Params: vtypes.Params{VerificationModuleActive: true},
		},
		attestationsResp: &vtypes.QueryProviderAttestationsResponse{
			Attestations: []vtypes.AttestationRecord{
				validAttestation("akash1auditor", vtypes.TierVerified),
			},
		},
		graceErr: status.Error(codes.NotFound, "provider verification grace not found"),
	}

	o := newVerificationPreflightTestOrder(t, query)
	require.True(t, o.shouldBidVerificationPreflight(context.Background(), &vtypes.VerificationRequirement{
		MinTier: vtypes.TierVerified,
	}))

	o = newVerificationPreflightTestOrder(t, query)
	require.False(t, o.shouldBidVerificationPreflight(context.Background(), &vtypes.VerificationRequirement{
		MinTier: vtypes.TierEstablished,
	}))
}

type verificationPreflightTestQueryClient struct {
	bidengineTestQueryClient
	verification vtypes.QueryClient
}

func (client verificationPreflightTestQueryClient) Verification() vtypes.QueryClient {
	return client.verification
}

type verificationPreflightQueryClient struct {
	vtypes.QueryClient

	paramsResp       *vtypes.QueryParamsResponse
	paramsErr        error
	attestationsResp *vtypes.QueryProviderAttestationsResponse
	attestationsErr  error
	graceResp        *vtypes.QueryProviderVerificationGraceResponse
	graceErr         error

	attestationsCalls int
	graceCalls        int
}

func (client *verificationPreflightQueryClient) Params(context.Context, *vtypes.QueryParamsRequest, ...grpc.CallOption) (*vtypes.QueryParamsResponse, error) {
	return client.paramsResp, client.paramsErr
}

func (client *verificationPreflightQueryClient) ProviderAttestations(context.Context, *vtypes.QueryProviderAttestationsRequest, ...grpc.CallOption) (*vtypes.QueryProviderAttestationsResponse, error) {
	client.attestationsCalls++
	return client.attestationsResp, client.attestationsErr
}

func (client *verificationPreflightQueryClient) ProviderVerificationGrace(context.Context, *vtypes.QueryProviderVerificationGraceRequest, ...grpc.CallOption) (*vtypes.QueryProviderVerificationGraceResponse, error) {
	client.graceCalls++
	return client.graceResp, client.graceErr
}

func newVerificationPreflightTestOrder(t *testing.T, verification vtypes.QueryClient) order {
	t.Helper()

	queryClient := verificationPreflightTestQueryClient{
		bidengineTestQueryClient: bidengineTestQueryClient{QueryClient: &clientmocks.QueryClient{}},
		verification:             verification,
	}

	client := &clientmocks.Client{}
	client.On("Query").Return(queryClient)

	logger := testutil.Logger(t)
	return order{
		session: session.New(logger, client, &ptypes.Provider{Owner: "akash1provider"}, -1),
		log:     logger,
	}
}

package bidengine

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/log"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	audittypes "pkg.akt.dev/go/node/audit/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

type mockAttrService struct {
	attrs       attrtypes.Attributes
	attrsErr    error
	auditedAttr audittypes.AuditedProviders
	auditedErr  error
}

func (m *mockAttrService) GetAttributes() (attrtypes.Attributes, error) {
	return m.attrs, m.attrsErr
}

func (m *mockAttrService) GetAuditorAttributeSignatures(_ string) (audittypes.AuditedProviders, error) {
	return m.auditedAttr, m.auditedErr
}

func makeMinimalGroupSpec() dvbeta.GroupSpec {
	cpu := rtypes.CPU{}
	cpu.Units = rtypes.NewResourceValue(uint64(dvbeta.GetValidationConfig().Unit.Min.CPU))

	gpu := rtypes.GPU{}
	gpu.Units = rtypes.NewResourceValue(uint64(dvbeta.GetValidationConfig().Unit.Min.GPU))

	memory := rtypes.Memory{}
	memory.Quantity = rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Memory)

	storage := rtypes.Volumes{
		rtypes.Storage{
			Quantity: rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Storage),
		},
	}

	return dvbeta.GroupSpec{
		Name: "test",
		Resources: dvbeta.ResourceUnits{
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
}

func defaultScreenBidParams() ScreenBidParams {
	return ScreenBidParams{
		Provider: &ptypes.Provider{
			Owner:      "akash1testprovider",
			Attributes: attrtypes.Attributes{},
		},
		Attributes:      attrtypes.Attributes{},
		MaxGroupVolumes: 1,
		AttrService:     &mockAttrService{},
		Log:             log.NewNopLogger(),
	}
}

func TestScreenBid_PassesWithMatchingAttributes(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Empty(t, result.Reasons)
}

func TestScreenBid_FailsIncompatibleProviderAttributes(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	gspec.Requirements.Attributes = attrtypes.Attributes{
		{Key: "region", Value: "us-west"},
	}
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Contains(t, result.Reasons, "incompatible provider attributes")
}

func TestScreenBid_FailsIncompatibleOrderAttributes(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.Attributes = attrtypes.Attributes{
		{Key: "tier", Value: "premium"},
	}

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Contains(t, result.Reasons, "incompatible order attributes")
}

func TestScreenBid_FailsMaxGroupVolumes(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	gspec.Resources[0].Storage = rtypes.Volumes{
		{Quantity: rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Storage)},
		{Quantity: rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Storage)},
		{Quantity: rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Storage)},
	}
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.MaxGroupVolumes = 2

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Len(t, result.Reasons, 1)
	require.Contains(t, result.Reasons[0], "group volumes count exceeds limit")
}

func TestScreenBid_FailsValidateBasic(t *testing.T) {
	gspec := dvbeta.GroupSpec{
		Name:      "",
		Resources: dvbeta.ResourceUnits{},
	}
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.False(t, result.Passed)

	hasValidationError := false
	for _, reason := range result.Reasons {
		if len(reason) > len("group validation error: ") {
			hasValidationError = true
			break
		}
	}
	require.True(t, hasValidationError, "expected a group validation error reason")
}

func TestScreenBid_CollectsMultipleReasons(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	gspec.Requirements.Attributes = attrtypes.Attributes{
		{Key: "region", Value: "us-west"},
	}
	gspec.Resources[0].Storage = rtypes.Volumes{
		{Quantity: rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Storage)},
		{Quantity: rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Storage)},
	}
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.MaxGroupVolumes = 1

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Greater(t, len(result.Reasons), 1, "expected multiple failure reasons")
}

func TestScreenBid_AttrServiceError(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.AttrService = &mockAttrService{
		attrsErr: errors.New("connection failed"),
	}

	_, err := ScreenBid(context.Background(), group, params)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching provider attributes")
}

func TestScreenBid_AuditorServiceError(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	gspec.Requirements.SignedBy.AnyOf = []string{"auditor1"}
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.AttrService = &mockAttrService{
		auditedErr: errors.New("auditor lookup failed"),
	}

	_, err := ScreenBid(context.Background(), group, params)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching auditor attribute signatures")
}

func TestScreenBid_AuditorSignatureRequirementsNotMet(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	gspec.Requirements.SignedBy.AnyOf = []string{"auditor1"}
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.AttrService = &mockAttrService{
		auditedAttr: audittypes.AuditedProviders{},
	}

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Contains(t, result.Reasons, "attribute signature requirements not met")
}

type mockHostnameService struct {
	ctypes.HostnameServiceClient
	err error
}

func (m *mockHostnameService) CanReserveHostnames(_ []string, _ sdk.Address) error {
	return m.err
}

func TestScreenBid_HostnamesAvailable(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.HostnameService = &mockHostnameService{}
	params.Owner = sdk.AccAddress("testowner")
	params.Hostnames = []string{"example.com"}

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Empty(t, result.Reasons)
}

func TestScreenBid_HostnamesUnavailable(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.HostnameService = &mockHostnameService{
		err: errors.New("hostname example.com already in use"),
	}
	params.Owner = sdk.AccAddress("testowner")
	params.Hostnames = []string{"example.com"}

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Len(t, result.Reasons, 1)
	require.Contains(t, result.Reasons[0], "hostnames unavailable")
}

func TestScreenBid_HostnamesSkippedWhenNoService(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.Hostnames = []string{"example.com"}

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Empty(t, result.Reasons)
}

func TestScreenBid_HostnamesSkippedWhenEmpty(t *testing.T) {
	gspec := makeMinimalGroupSpec()
	group := &dvbeta.Group{GroupSpec: gspec}
	params := defaultScreenBidParams()
	params.HostnameService = &mockHostnameService{
		err: errors.New("should not be called"),
	}
	params.Hostnames = nil

	result, err := ScreenBid(context.Background(), group, params)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Empty(t, result.Reasons)
}

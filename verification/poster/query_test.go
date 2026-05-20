package poster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	verificationv1 "pkg.akt.dev/go/node/verification/v1"
)

type testVerificationQueryClient struct {
	verificationv1.QueryClient

	paramsResp   *verificationv1.QueryParamsResponse
	paramsErr    error
	snapshotResp *verificationv1.QueryProviderSnapshotResponse
	snapshotErr  error

	provider string
}

func (q *testVerificationQueryClient) Params(context.Context, *verificationv1.QueryParamsRequest, ...grpc.CallOption) (*verificationv1.QueryParamsResponse, error) {
	return q.paramsResp, q.paramsErr
}

func (q *testVerificationQueryClient) ProviderSnapshot(_ context.Context, req *verificationv1.QueryProviderSnapshotRequest, _ ...grpc.CallOption) (*verificationv1.QueryProviderSnapshotResponse, error) {
	q.provider = req.GetProvider()
	return q.snapshotResp, q.snapshotErr
}

func TestNewQueryAdapterValidatesClient(t *testing.T) {
	query, err := NewQueryAdapter(nil)
	require.ErrorIs(t, err, errMissingQueryClient)
	require.Nil(t, query)
}

func TestQueryAdapterParams(t *testing.T) {
	expected := verificationv1.Params{
		SnapshotHashInterval:     time.Hour,
		VerificationModuleActive: true,
	}
	adapter, err := NewQueryAdapter(&testVerificationQueryClient{
		paramsResp: &verificationv1.QueryParamsResponse{Params: expected},
	})
	require.NoError(t, err)

	params, err := adapter.Params(context.Background())
	require.NoError(t, err)
	require.Equal(t, expected, params)
}

func TestQueryAdapterParamsReturnsErrors(t *testing.T) {
	expected := errors.New("params failed")
	adapter, err := NewQueryAdapter(&testVerificationQueryClient{paramsErr: expected})
	require.NoError(t, err)

	params, err := adapter.Params(context.Background())
	require.ErrorIs(t, err, expected)
	require.Equal(t, verificationv1.Params{}, params)

	adapter, err = NewQueryAdapter(&testVerificationQueryClient{})
	require.NoError(t, err)

	params, err = adapter.Params(context.Background())
	require.ErrorIs(t, err, errMissingParamsResponse)
	require.Equal(t, verificationv1.Params{}, params)
}

func TestQueryAdapterProviderSnapshot(t *testing.T) {
	expected := verificationv1.ProviderSnapshotRecord{
		Provider:           "akash1provider",
		SnapshotHash:       []byte("hash"),
		ComplianceDeadline: time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC),
	}
	client := &testVerificationQueryClient{
		snapshotResp: &verificationv1.QueryProviderSnapshotResponse{Snapshot: expected},
	}
	adapter, err := NewQueryAdapter(client)
	require.NoError(t, err)

	record, err := adapter.ProviderSnapshot(context.Background(), expected.Provider)
	require.NoError(t, err)
	require.Equal(t, expected.Provider, client.provider)
	require.Equal(t, expected, *record)
}

func TestQueryAdapterProviderSnapshotNotFound(t *testing.T) {
	tests := []struct {
		name string
		resp *verificationv1.QueryProviderSnapshotResponse
		err  error
	}{
		{
			name: "not found status",
			err:  status.Error(codes.NotFound, "provider snapshot not found"),
		},
		{
			name: "nil response",
		},
		{
			name: "empty record",
			resp: &verificationv1.QueryProviderSnapshotResponse{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := NewQueryAdapter(&testVerificationQueryClient{
				snapshotResp: test.resp,
				snapshotErr:  test.err,
			})
			require.NoError(t, err)

			record, err := adapter.ProviderSnapshot(context.Background(), "akash1provider")
			require.ErrorIs(t, err, ErrProviderSnapshotNotFound)
			require.Nil(t, record)
		})
	}
}

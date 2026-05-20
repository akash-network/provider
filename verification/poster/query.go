package poster

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	verificationv1 "pkg.akt.dev/go/node/verification/v1"
)

var errMissingParamsResponse = errors.New("missing verification params response")

type queryAdapter struct {
	client verificationv1.QueryClient
}

func NewQueryAdapter(client verificationv1.QueryClient) (QueryClient, error) {
	if client == nil {
		return nil, errMissingQueryClient
	}

	return queryAdapter{client: client}, nil
}

func (q queryAdapter) Params(ctx context.Context) (verificationv1.Params, error) {
	resp, err := q.client.Params(ctx, &verificationv1.QueryParamsRequest{})
	if err != nil {
		return verificationv1.Params{}, err
	}
	if resp == nil {
		return verificationv1.Params{}, errMissingParamsResponse
	}

	return resp.GetParams(), nil
}

func (q queryAdapter) ProviderSnapshot(ctx context.Context, provider string) (*verificationv1.ProviderSnapshotRecord, error) {
	resp, err := q.client.ProviderSnapshot(ctx, &verificationv1.QueryProviderSnapshotRequest{
		Provider: provider,
	})
	if status.Code(err) == codes.NotFound {
		return nil, ErrProviderSnapshotNotFound
	}
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, ErrProviderSnapshotNotFound
	}

	record := resp.GetSnapshot()
	if record.GetProvider() == "" {
		return nil, ErrProviderSnapshotNotFound
	}

	return &record, nil
}

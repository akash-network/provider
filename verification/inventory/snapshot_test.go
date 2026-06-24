package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"k8s.io/apimachinery/pkg/api/resource"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"
	providerv1 "pkg.akt.dev/go/provider/v1"
	"pkg.akt.dev/go/testutil"

	ptypes "github.com/akash-network/provider/types"
)

type testPayloadSource struct {
	payload []byte
	err     error
	req     SnapshotRequest
	called  bool
}

func (s *testPayloadSource) Payload(_ context.Context, req SnapshotRequest) ([]byte, error) {
	s.called = true
	s.req = req

	return append([]byte(nil), s.payload...), s.err
}

type testSigner struct {
	address sdk.AccAddress
	err     error

	signature []byte
	payload   []byte
	called    bool
}

func (s *testSigner) Address() sdk.AccAddress {
	return s.address
}

func (s *testSigner) Sign(_ context.Context, payload []byte) ([]byte, error) {
	s.called = true
	s.payload = append([]byte(nil), payload...)
	if s.err != nil {
		return nil, s.err
	}
	if s.signature != nil {
		return append([]byte(nil), s.signature...), nil
	}

	return []byte("signature"), nil
}

func (s *testSigner) Broadcast(context.Context, ...sdk.Msg) (*sdk.TxResponse, error) {
	return nil, errors.New("unused")
}

func TestNewBuilderValidatesDependencies(t *testing.T) {
	signer := &testSigner{address: testutil.AccAddress(t)}
	payload := &testPayloadSource{}

	tests := []struct {
		name    string
		payload PayloadSource
		signer  ptypes.ProviderSigner
		wantErr error
	}{
		{
			name:    "success",
			payload: payload,
			signer:  signer,
		},
		{
			name:    "missing payload",
			signer:  signer,
			wantErr: errMissingPayloadSource,
		},
		{
			name:    "missing signer",
			payload: payload,
			wantErr: errMissingSigner,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewBuilder(test.payload, test.signer)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				require.Nil(t, builder)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, builder)
		})
	}
}

func TestValidateNonce(t *testing.T) {
	tests := []struct {
		name    string
		nonce   []byte
		wantErr bool
	}{
		{
			name: "nil",
		},
		{
			name:  "empty",
			nonce: []byte{},
		},
		{
			name:  "valid",
			nonce: bytes.Repeat([]byte{1}, NonceSize),
		},
		{
			name:    "short",
			nonce:   bytes.Repeat([]byte{1}, NonceSize-1),
			wantErr: true,
		},
		{
			name:    "long",
			nonce:   bytes.Repeat([]byte{1}, NonceSize+1),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateNonce(test.nonce)
			if test.wantErr {
				require.ErrorIs(t, err, errInvalidNonce)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestHashPayload(t *testing.T) {
	payload := []byte("snapshot")
	expected := sha256.Sum256(payload)

	require.Equal(t, expected[:], HashPayload(payload))
}

func TestHashPayloadIncludesChallengeFields(t *testing.T) {
	first, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		Nonce:         bytes.Repeat([]byte{1}, NonceSize),
		Timestamp:     time.Unix(1, 0).UTC(),
		ResourceSummary: inventoryv1.SnapshotResourceSummary{
			TotalVCPUs: 32,
		},
	})
	require.NoError(t, err)

	second, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		Nonce:         bytes.Repeat([]byte{2}, NonceSize),
		Timestamp:     time.Unix(2, 0).UTC(),
		ResourceSummary: inventoryv1.SnapshotResourceSummary{
			TotalVCPUs: 32,
		},
	})
	require.NoError(t, err)

	require.NotEqual(t, HashPayload(first), HashPayload(second))
}

func TestHashPayloadIncludesPayloadFields(t *testing.T) {
	first, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		Nonce:         bytes.Repeat([]byte{1}, NonceSize),
		Timestamp:     time.Unix(1, 0).UTC(),
		Cluster: inventoryv1.Cluster{
			Nodes: []inventoryv1.Node{{
				Name: "node-1",
				Resources: inventoryv1.NodeResources{
					CPU: inventoryv1.CPU{Quantity: inventoryv1.ResourcePair{
						Allocatable: quantity("32"),
						Allocated:   quantity("1250m"),
						Capacity:    quantity("32"),
					}},
					Memory: inventoryv1.Memory{Quantity: inventoryv1.ResourcePair{
						Allocatable: quantity("128Gi"),
						Allocated:   quantity("708Mi"),
						Capacity:    quantity("128Gi"),
					}},
				},
			}},
		},
		EvidenceSections: []inventoryv1.SnapshotEvidenceSection{{
			Name:    "test",
			Payload: []byte("first"),
		}},
	})
	require.NoError(t, err)

	second, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		Nonce:         bytes.Repeat([]byte{2}, NonceSize),
		Timestamp:     time.Unix(2, 0).UTC(),
		Cluster: inventoryv1.Cluster{
			Nodes: []inventoryv1.Node{{
				Name: "node-1",
				Resources: inventoryv1.NodeResources{
					CPU: inventoryv1.CPU{Quantity: inventoryv1.ResourcePair{
						Allocatable: quantity("32"),
						Allocated:   quantity("2500m"),
						Capacity:    quantity("32"),
					}},
					Memory: inventoryv1.Memory{Quantity: inventoryv1.ResourcePair{
						Allocatable: quantity("128Gi"),
						Allocated:   quantity("1Gi"),
						Capacity:    quantity("128Gi"),
					}},
				},
			}},
		},
		EvidenceSections: []inventoryv1.SnapshotEvidenceSection{{
			Name:    "test",
			Payload: []byte("second"),
		}},
	})
	require.NoError(t, err)

	require.NotEqual(t, HashPayload(first), HashPayload(second))
}

func TestHashPayloadIncludesInventoryMaterial(t *testing.T) {
	first, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		ResourceSummary: inventoryv1.SnapshotResourceSummary{
			TotalVCPUs: 32,
		},
	})
	require.NoError(t, err)

	second, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		ResourceSummary: inventoryv1.SnapshotResourceSummary{
			TotalVCPUs: 64,
		},
	})
	require.NoError(t, err)

	require.NotEqual(t, HashPayload(first), HashPayload(second))
}

func TestHashPayloadIncludesCapacityMaterial(t *testing.T) {
	first, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		Cluster: inventoryv1.Cluster{
			Nodes: []inventoryv1.Node{{
				Name: "node-1",
				Resources: inventoryv1.NodeResources{
					CPU: inventoryv1.CPU{Quantity: inventoryv1.ResourcePair{
						Allocatable: quantity("32"),
						Capacity:    quantity("32"),
					}},
				},
			}},
		},
	})
	require.NoError(t, err)

	second, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		Cluster: inventoryv1.Cluster{
			Nodes: []inventoryv1.Node{{
				Name: "node-1",
				Resources: inventoryv1.NodeResources{
					CPU: inventoryv1.CPU{Quantity: inventoryv1.ResourcePair{
						Allocatable: quantity("64"),
						Capacity:    quantity("64"),
					}},
				},
			}},
		},
	})
	require.NoError(t, err)

	require.NotEqual(t, HashPayload(first), HashPayload(second))
}

func TestMarshalDeterministic(t *testing.T) {
	msg := &providerv1.Status{
		PublicHostnames: []string{"provider.example.com"},
	}

	first, err := MarshalDeterministic(msg)
	require.NoError(t, err)
	second, err := MarshalDeterministic(msg)
	require.NoError(t, err)

	require.NotEmpty(t, first)
	require.Equal(t, first, second)
}

func TestBuilderBuild(t *testing.T) {
	nonce := bytes.Repeat([]byte{1}, NonceSize)
	payload := []byte("canonical payload")
	source := &testPayloadSource{payload: payload}
	signer := &testSigner{address: testutil.AccAddress(t)}
	builder, err := NewBuilder(source, signer)
	require.NoError(t, err)

	snapshot, err := builder.Build(context.Background(), SnapshotRequest{Nonce: nonce})
	require.NoError(t, err)

	require.True(t, source.called)
	require.Equal(t, nonce, source.req.Nonce)
	require.True(t, signer.called)
	require.Equal(t, payload, signer.payload)
	require.Equal(t, payload, snapshot.Payload)
	require.Equal(t, HashPayload(payload), snapshot.Hash)
	require.Equal(t, []byte("signature"), snapshot.Signature)
	require.Equal(t, signer.address.String(), snapshot.Provider)
}

func TestValidateSnapshot(t *testing.T) {
	valid := &Snapshot{
		Payload:   []byte("payload"),
		Hash:      []byte("hash"),
		Signature: []byte("signature"),
		Provider:  "akash1provider",
	}

	tests := []struct {
		name     string
		snapshot *Snapshot
		wantErr  error
	}{
		{
			name:     "success",
			snapshot: valid,
		},
		{
			name:    "nil snapshot",
			wantErr: errMissingSnapshot,
		},
		{
			name: "missing payload",
			snapshot: &Snapshot{
				Hash:      valid.Hash,
				Signature: valid.Signature,
				Provider:  valid.Provider,
			},
			wantErr: errMissingPayload,
		},
		{
			name: "missing hash",
			snapshot: &Snapshot{
				Payload:   valid.Payload,
				Signature: valid.Signature,
				Provider:  valid.Provider,
			},
			wantErr: errMissingHash,
		},
		{
			name: "missing signature",
			snapshot: &Snapshot{
				Payload:  valid.Payload,
				Hash:     valid.Hash,
				Provider: valid.Provider,
			},
			wantErr: errMissingSignature,
		},
		{
			name: "missing provider",
			snapshot: &Snapshot{
				Payload:   valid.Payload,
				Hash:      valid.Hash,
				Signature: valid.Signature,
			},
			wantErr: errMissingProvider,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSnapshot(test.snapshot)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
		})
	}
}

func quantity(val string) *resource.Quantity {
	q := resource.MustParse(val)
	return &q
}

func TestBuilderBuildRejectsInvalidNonce(t *testing.T) {
	source := &testPayloadSource{}
	signer := &testSigner{address: testutil.AccAddress(t)}
	builder, err := NewBuilder(source, signer)
	require.NoError(t, err)

	snapshot, err := builder.Build(context.Background(), SnapshotRequest{Nonce: bytes.Repeat([]byte{1}, NonceSize-1)})
	require.ErrorIs(t, err, errInvalidNonce)
	require.Nil(t, snapshot)
	require.False(t, source.called)
	require.False(t, signer.called)
}

func TestBuilderBuildRejectsEmptyPayload(t *testing.T) {
	source := &testPayloadSource{}
	signer := &testSigner{address: testutil.AccAddress(t)}
	builder, err := NewBuilder(source, signer)
	require.NoError(t, err)

	snapshot, err := builder.Build(context.Background(), SnapshotRequest{})
	require.ErrorIs(t, err, errMissingPayload)
	require.Nil(t, snapshot)
	require.True(t, source.called)
	require.False(t, signer.called)
}

func TestBuilderBuildReturnsPayloadError(t *testing.T) {
	payloadErr := errors.New("payload failed")
	source := &testPayloadSource{err: payloadErr}
	signer := &testSigner{address: testutil.AccAddress(t)}
	builder, err := NewBuilder(source, signer)
	require.NoError(t, err)

	snapshot, err := builder.Build(context.Background(), SnapshotRequest{})
	require.ErrorIs(t, err, payloadErr)
	require.Nil(t, snapshot)
	require.False(t, signer.called)
}

func TestBuilderBuildReturnsSignError(t *testing.T) {
	signErr := errors.New("sign failed")
	source := &testPayloadSource{payload: []byte("payload")}
	signer := &testSigner{
		address: testutil.AccAddress(t),
		err:     signErr,
	}
	builder, err := NewBuilder(source, signer)
	require.NoError(t, err)

	snapshot, err := builder.Build(context.Background(), SnapshotRequest{})
	require.ErrorIs(t, err, signErr)
	require.Nil(t, snapshot)
}

func TestBuilderBuildRejectsEmptySignature(t *testing.T) {
	source := &testPayloadSource{payload: []byte("payload")}
	signer := &testSigner{
		address:   testutil.AccAddress(t),
		signature: []byte{},
	}
	builder, err := NewBuilder(source, signer)
	require.NoError(t, err)

	snapshot, err := builder.Build(context.Background(), SnapshotRequest{})
	require.ErrorIs(t, err, errMissingSignature)
	require.Nil(t, snapshot)
}

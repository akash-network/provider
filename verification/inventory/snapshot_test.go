package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

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

	payload []byte
	called  bool
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

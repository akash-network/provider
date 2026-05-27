package inventory

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	ptypes "github.com/akash-network/provider/types"
)

const NonceSize = 32

var (
	errInvalidNonce         = errors.New("invalid inventory snapshot nonce")
	errMissingPayloadSource = errors.New("missing inventory snapshot payload source")
	errMissingSigner        = errors.New("missing inventory snapshot signer")
)

type Collector interface {
	Name() string
	Collect(context.Context) (EvidenceSection, error)
}

type EvidenceSection struct {
	Name    string
	Payload []byte
}

type PayloadSource interface {
	Payload(context.Context, SnapshotRequest) ([]byte, error)
}

type SnapshotRequest struct {
	Nonce []byte
}

type Snapshot struct {
	Payload   []byte
	Hash      []byte
	Signature []byte
	Provider  string
}

type Builder struct {
	payload PayloadSource
	signer  ptypes.ProviderSigner
}

type deterministicMarshaler interface {
	XXX_Size() int
	XXX_Marshal([]byte, bool) ([]byte, error)
}

func NewBuilder(payload PayloadSource, signer ptypes.ProviderSigner) (*Builder, error) {
	if payload == nil {
		return nil, errMissingPayloadSource
	}

	if signer == nil {
		return nil, errMissingSigner
	}

	return &Builder{
		payload: payload,
		signer:  signer,
	}, nil
}

func ValidateNonce(nonce []byte) error {
	if len(nonce) == 0 {
		return nil
	}

	if len(nonce) != NonceSize {
		return fmt.Errorf("%w: expected 0 or %d bytes, got %d", errInvalidNonce, NonceSize, len(nonce))
	}

	return nil
}

func HashPayload(payload []byte) []byte {
	hash := sha256.Sum256(payload)
	return hash[:]
}

func MarshalDeterministic(msg proto.Message) ([]byte, error) {
	if msg, ok := msg.(deterministicMarshaler); ok {
		payload, err := msg.XXX_Marshal(make([]byte, 0, msg.XXX_Size()), true)
		if err != nil {
			return nil, err
		}

		return append([]byte(nil), payload...), nil
	}

	var buf proto.Buffer

	buf.SetDeterministic(true)
	if err := buf.Marshal(msg); err != nil {
		return nil, err
	}

	return append([]byte(nil), buf.Bytes()...), nil
}

func (b *Builder) Build(ctx context.Context, req SnapshotRequest) (*Snapshot, error) {
	if err := ValidateNonce(req.Nonce); err != nil {
		return nil, err
	}

	req.Nonce = append([]byte(nil), req.Nonce...)

	payload, err := b.payload.Payload(ctx, req)
	if err != nil {
		return nil, err
	}

	hash := HashPayload(payload)

	signature, err := b.signer.Sign(ctx, payload)
	if err != nil {
		return nil, err
	}

	return &Snapshot{
		Payload:   append([]byte(nil), payload...),
		Hash:      hash,
		Signature: append([]byte(nil), signature...),
		Provider:  b.signer.Address().String(),
	}, nil
}

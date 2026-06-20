package inventory

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/cosmos/gogoproto/proto"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"

	ptypes "github.com/akash-network/provider/types"
)

const NonceSize = 32

var (
	errInvalidNonce         = errors.New("invalid inventory snapshot nonce")
	errMissingPayloadSource = errors.New("missing inventory snapshot payload source")
	errMissingSigner        = errors.New("missing inventory snapshot signer")
	errMissingSnapshot      = errors.New("missing inventory snapshot")
	errMissingPayload       = errors.New("missing inventory snapshot payload")
	errMissingHash          = errors.New("missing inventory snapshot hash")
	errMissingSignature     = errors.New("missing inventory snapshot signature")
	errMissingProvider      = errors.New("missing inventory snapshot provider")
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
	payload = snapshotHashPayload(payload)
	hash := sha256.Sum256(payload)
	return append([]byte(nil), hash[:]...)
}

func snapshotHashPayload(payload []byte) []byte {
	var snapshot inventoryv1.SnapshotPayload
	if err := proto.Unmarshal(payload, &snapshot); err != nil {
		return payload
	}
	if !isSnapshotPayload(snapshot) {
		return payload
	}

	normalizeSnapshotPayloadForHash(&snapshot)

	canonical, err := MarshalDeterministic(&snapshot)
	if err != nil {
		return payload
	}

	return canonical
}

func isSnapshotPayload(payload inventoryv1.SnapshotPayload) bool {
	return payload.SchemaVersion != 0 && payload.Provider != "" && payload.ChainID != ""
}

func normalizeSnapshotPayloadForHash(payload *inventoryv1.SnapshotPayload) {
	payload.Nonce = nil
	payload.Timestamp = time.Time{}
	payload.EvidenceSections = nil

	normalizeClusterForHash(&payload.Cluster)
}

func normalizeClusterForHash(cluster *inventoryv1.Cluster) {
	for idx := range cluster.Nodes {
		normalizeNodeResourcesForHash(&cluster.Nodes[idx].Resources)
	}
	for idx := range cluster.Storage {
		normalizeResourcePairForHash(&cluster.Storage[idx].Quantity)
	}
}

func normalizeNodeResourcesForHash(resources *inventoryv1.NodeResources) {
	normalizeResourcePairForHash(&resources.CPU.Quantity)
	normalizeResourcePairForHash(&resources.Memory.Quantity)
	normalizeResourcePairForHash(&resources.GPU.Quantity)
	normalizeResourcePairForHash(&resources.EphemeralStorage)
	normalizeResourcePairForHash(&resources.VolumesAttached)
	normalizeResourcePairForHash(&resources.VolumesMounted)
}

func normalizeResourcePairForHash(pair *inventoryv1.ResourcePair) {
	pair.Allocated = nil
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
	if len(payload) == 0 {
		return nil, errMissingPayload
	}

	hash := HashPayload(payload)

	signature, err := b.signer.Sign(ctx, payload)
	if err != nil {
		return nil, err
	}

	snapshot := &Snapshot{
		Payload:   append([]byte(nil), payload...),
		Hash:      hash,
		Signature: append([]byte(nil), signature...),
		Provider:  b.signer.Address().String(),
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		return nil, err
	}

	return snapshot, nil
}

func ValidateSnapshot(snapshot *Snapshot) error {
	if snapshot == nil {
		return errMissingSnapshot
	}

	if len(snapshot.Payload) == 0 {
		return errMissingPayload
	}

	if len(snapshot.Hash) == 0 {
		return errMissingHash
	}

	if len(snapshot.Signature) == 0 {
		return errMissingSignature
	}

	if snapshot.Provider == "" {
		return errMissingProvider
	}

	return nil
}

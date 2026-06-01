package inventory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"
	providerv1 "pkg.akt.dev/go/provider/v1"
)

const (
	SnapshotPayloadSchemaVersion  uint32 = 1
	EvidenceSectionProviderStatus        = "akash.provider.v1.Status"
)

var (
	errMissingStatusClient    = errors.New("missing provider status client")
	errMissingProviderAddress = errors.New("missing provider address")
	errMissingChainID         = errors.New("missing chain ID")
	errMissingProviderStatus  = errors.New("missing provider status")
	errMissingProviderCluster = errors.New("missing provider cluster status")
	errMissingSnapshotClock   = errors.New("missing inventory snapshot clock")
)

type StatusClient interface {
	StatusV1(context.Context) (*providerv1.Status, error)
}

type StatusPayloadSourceConfig struct {
	Status            StatusClient
	Provider          string
	ChainID           string
	SoftwareVersion   string
	SoftwareSignature []byte
	SoftwareIdentity  *inventoryv1.SoftwareIdentity
	Now               func() time.Time
	Collectors        []Collector
}

type StatusPayloadSource struct {
	status            StatusClient
	provider          string
	chainID           string
	softwareVersion   string
	softwareSignature []byte
	softwareIdentity  *inventoryv1.SoftwareIdentity
	now               func() time.Time
	collectors        []Collector
}

func NewStatusPayloadSource(cfg StatusPayloadSourceConfig) (*StatusPayloadSource, error) {
	if cfg.Status == nil {
		return nil, errMissingStatusClient
	}

	if cfg.Provider == "" {
		return nil, errMissingProviderAddress
	}

	if cfg.ChainID == "" {
		return nil, errMissingChainID
	}

	if cfg.Now == nil {
		return nil, errMissingSnapshotClock
	}

	return &StatusPayloadSource{
		status:            cfg.Status,
		provider:          cfg.Provider,
		chainID:           cfg.ChainID,
		softwareVersion:   cfg.SoftwareVersion,
		softwareSignature: append([]byte(nil), cfg.SoftwareSignature...),
		softwareIdentity:  cloneSoftwareIdentity(cfg.SoftwareIdentity),
		now:               cfg.Now,
		collectors:        append([]Collector(nil), cfg.Collectors...),
	}, nil
}

func (s *StatusPayloadSource) Payload(ctx context.Context, req SnapshotRequest) ([]byte, error) {
	status, err := s.status.StatusV1(ctx)
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, errMissingProviderStatus
	}

	clusterStatus := status.GetCluster()
	if clusterStatus == nil {
		return nil, errMissingProviderCluster
	}

	statusInventory := clusterStatus.GetInventory()
	cluster := statusInventory.GetCluster()
	evidence, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	statusEvidence, err := statusEvidenceSection(status)
	if err != nil {
		return nil, err
	}
	evidence = append([]inventoryv1.SnapshotEvidenceSection{statusEvidence}, evidence...)
	leases := clusterStatus.GetLeases()

	payload := &inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      s.provider,
		ChainID:       s.chainID,
		Nonce:         append([]byte(nil), req.Nonce...),
		Timestamp:     s.now().UTC(),
		Cluster:       cluster,
		ResourceSummary: ResourceSummaryFromCluster(
			cluster,
			leases.GetActive(),
			s.softwareVersion,
			s.softwareSignature,
			s.softwareIdentity,
		),
		EvidenceSections: evidence,
	}

	return MarshalDeterministic(payload)
}

func statusEvidenceSection(status *providerv1.Status) (inventoryv1.SnapshotEvidenceSection, error) {
	payload, err := MarshalDeterministic(status)
	if err != nil {
		return inventoryv1.SnapshotEvidenceSection{}, err
	}

	return inventoryv1.SnapshotEvidenceSection{
		Name:    EvidenceSectionProviderStatus,
		Payload: payload,
	}, nil
}

func (s *StatusPayloadSource) collect(ctx context.Context) ([]inventoryv1.SnapshotEvidenceSection, error) {
	sections := make([]inventoryv1.SnapshotEvidenceSection, 0, len(s.collectors))
	for _, collector := range s.collectors {
		if collector == nil {
			continue
		}

		section, err := collector.Collect(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", collector.Name(), err)
		}

		name := section.Name
		if name == "" {
			name = collector.Name()
		}

		sections = append(sections, inventoryv1.SnapshotEvidenceSection{
			Name:    name,
			Payload: append([]byte(nil), section.Payload...),
		})
	}

	return sections, nil
}

func ResourceSummaryFromCluster(
	cluster inventoryv1.Cluster,
	activeLeases uint32,
	softwareVersion string,
	softwareSignature []byte,
	softwareIdentity *inventoryv1.SoftwareIdentity,
) inventoryv1.SnapshotResourceSummary {
	var (
		totalCPUMilli         uint64
		totalGPUs             uint64
		totalMemoryBytes      uint64
		totalEphemeralStorage uint64
		totalStorageBytes     uint64
	)

	for _, node := range cluster.Nodes {
		resources := node.GetResources()
		cpu := resources.GetCPU()
		cpuQuantity := cpu.GetQuantity()
		totalCPUMilli += quantityMilliValue(cpuQuantity.GetAllocatable())

		gpu := resources.GetGPU()
		gpuQuantity := gpu.GetQuantity()
		totalGPUs += quantityValue(gpuQuantity.GetAllocatable())

		memory := resources.GetMemory()
		memoryQuantity := memory.GetQuantity()
		totalMemoryBytes += quantityValue(memoryQuantity.GetAllocatable())

		ephemeralStorage := resources.GetEphemeralStorage()
		totalEphemeralStorage += quantityValue(ephemeralStorage.GetAllocatable())
	}

	for _, storage := range cluster.Storage {
		storageQuantity := storage.GetQuantity()
		totalStorageBytes += quantityValue(storageQuantity.GetAllocatable())
	}

	return inventoryv1.SnapshotResourceSummary{
		TotalGPUs:         saturatingUint32(totalGPUs),
		TotalVCPUs:        milliCPUToVCPUs(totalCPUMilli),
		TotalMemoryMB:     bytesToMiB(totalMemoryBytes),
		TotalStorageMB:    bytesToMiB(totalEphemeralStorage + totalStorageBytes),
		ActiveLeases:      activeLeases,
		SoftwareVersion:   softwareVersion,
		SoftwareSignature: append([]byte(nil), softwareSignature...),
		SoftwareIdentity:  cloneSoftwareIdentity(softwareIdentity),
	}
}

func cloneSoftwareIdentity(identity *inventoryv1.SoftwareIdentity) *inventoryv1.SoftwareIdentity {
	if identity == nil {
		return nil
	}

	return &inventoryv1.SoftwareIdentity{
		Version:         identity.GetVersion(),
		ArtifactRef:     identity.GetArtifactRef(),
		DigestAlgorithm: identity.GetDigestAlgorithm(),
		Digest:          append([]byte(nil), identity.GetDigest()...),
		SignatureType:   identity.GetSignatureType(),
		Signature:       append([]byte(nil), identity.GetSignature()...),
		SignatureRef:    identity.GetSignatureRef(),
		PublicKeyRef:    identity.GetPublicKeyRef(),
	}
}

func quantityValue(quantity *resource.Quantity) uint64 {
	if quantity == nil {
		return 0
	}

	value := quantity.Value()
	if value <= 0 {
		return 0
	}

	return uint64(value) // nolint: gosec
}

func quantityMilliValue(quantity *resource.Quantity) uint64 {
	if quantity == nil {
		return 0
	}

	value := quantity.MilliValue()
	if value <= 0 {
		return 0
	}

	return uint64(value) // nolint: gosec
}

func milliCPUToVCPUs(milli uint64) uint32 {
	vcpus := milli / 1000
	if milli%1000 != 0 {
		vcpus++
	}

	return saturatingUint32(vcpus)
}

func bytesToMiB(value uint64) uint64 {
	return value / (1024 * 1024)
}

func saturatingUint32(value uint64) uint32 {
	if value > math.MaxUint32 {
		return math.MaxUint32
	}

	return uint32(value) // nolint: gosec
}

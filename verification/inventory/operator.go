package inventory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cinventory "github.com/akash-network/provider/cluster/types/v1beta3/clients/inventory"
)

const EvidenceSectionInventoryOperator = "akash.provider.inventory.operator.v1.Cluster"

var (
	errMissingInventoryClient = errors.New("missing inventory client")
	errInventoryNotReady      = errors.New("inventory snapshot material not ready")
)

type OperatorMaterialSourceConfig struct {
	Inventory         cinventory.Client
	Status            StatusClient
	SoftwareVersion   string
	SoftwareSignature []byte
	SoftwareIdentity  *inventoryv1.SoftwareIdentity
	Collectors        []Collector
}

type OperatorMaterialSource struct {
	status            StatusClient
	softwareVersion   string
	softwareSignature []byte
	softwareIdentity  *inventoryv1.SoftwareIdentity
	collectors        []Collector

	lock    sync.RWMutex
	cluster inventoryv1.Cluster
	ready   chan struct{}
	has     bool
}

func NewOperatorMaterialSource(ctx context.Context, cfg OperatorMaterialSourceConfig) (*OperatorMaterialSource, error) {
	if cfg.Inventory == nil {
		return nil, errMissingInventoryClient
	}

	source := &OperatorMaterialSource{
		status:            cfg.Status,
		softwareVersion:   cfg.SoftwareVersion,
		softwareSignature: append([]byte(nil), cfg.SoftwareSignature...),
		softwareIdentity:  cloneSoftwareIdentity(cfg.SoftwareIdentity),
		collectors:        append([]Collector(nil), cfg.Collectors...),
		ready:             make(chan struct{}),
	}

	go source.watch(ctx, cfg.Inventory.ResultChan())

	return source, nil
}

func (s *OperatorMaterialSource) SnapshotMaterial(ctx context.Context) (SnapshotMaterial, error) {
	cluster, err := s.snapshot(ctx)
	if err != nil {
		return SnapshotMaterial{}, err
	}

	evidence, err := s.collect(ctx)
	if err != nil {
		return SnapshotMaterial{}, err
	}

	operatorEvidence, err := operatorEvidenceSection(cluster)
	if err != nil {
		return SnapshotMaterial{}, err
	}
	evidence = append([]inventoryv1.SnapshotEvidenceSection{operatorEvidence}, evidence...)

	var activeLeases uint32
	if s.status != nil {
		status, err := s.status.StatusV1(ctx)
		if err != nil {
			return SnapshotMaterial{}, err
		}
		if status == nil {
			return SnapshotMaterial{}, errMissingProviderStatus
		}

		clusterStatus := status.GetCluster()
		if clusterStatus == nil {
			return SnapshotMaterial{}, errMissingProviderCluster
		}

		leases := clusterStatus.GetLeases()
		activeLeases = leases.GetActive()
		statusEvidence, err := statusEvidenceSection(status)
		if err != nil {
			return SnapshotMaterial{}, err
		}
		evidence = append(evidence, statusEvidence)
	}

	return SnapshotMaterial{
		Cluster:           cluster,
		ActiveLeases:      activeLeases,
		EvidenceSections:  evidence,
		SoftwareVersion:   s.softwareVersion,
		SoftwareSignature: append([]byte(nil), s.softwareSignature...),
		SoftwareIdentity:  cloneSoftwareIdentity(s.softwareIdentity),
	}, nil
}

func (s *OperatorMaterialSource) watch(ctx context.Context, ch <-chan ctypes.Inventory) {
	for {
		select {
		case <-ctx.Done():
			return
		case inv, ok := <-ch:
			if !ok {
				return
			}
			if inv == nil {
				continue
			}

			s.setSnapshot(inv.Snapshot())
		}
	}
}

func (s *OperatorMaterialSource) snapshot(ctx context.Context) (inventoryv1.Cluster, error) {
	s.lock.RLock()
	if s.has {
		cluster := s.cluster
		s.lock.RUnlock()
		return cluster, nil
	}
	ready := s.ready
	s.lock.RUnlock()

	select {
	case <-ctx.Done():
		return inventoryv1.Cluster{}, ctx.Err()
	case <-ready:
	}

	s.lock.RLock()
	defer s.lock.RUnlock()
	if !s.has {
		return inventoryv1.Cluster{}, errInventoryNotReady
	}

	return s.cluster, nil
}

func (s *OperatorMaterialSource) setSnapshot(cluster inventoryv1.Cluster) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.cluster = cluster
	if !s.has {
		s.has = true
		close(s.ready)
	}
}

func (s *OperatorMaterialSource) collect(ctx context.Context) ([]inventoryv1.SnapshotEvidenceSection, error) {
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

func operatorEvidenceSection(cluster inventoryv1.Cluster) (inventoryv1.SnapshotEvidenceSection, error) {
	payload, err := MarshalDeterministic(&cluster)
	if err != nil {
		return inventoryv1.SnapshotEvidenceSection{}, err
	}

	return inventoryv1.SnapshotEvidenceSection{
		Name:    EvidenceSectionInventoryOperator,
		Payload: payload,
	}, nil
}

var _ SnapshotMaterialSource = (*OperatorMaterialSource)(nil)
var _ SnapshotMaterialSource = (*StatusPayloadSource)(nil)

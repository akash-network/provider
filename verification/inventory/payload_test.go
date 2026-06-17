package inventory

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"
	providerv1 "pkg.akt.dev/go/provider/v1"
)

type testStatusClient struct {
	status *providerv1.Status
	err    error
}

func (s testStatusClient) StatusV1(context.Context) (*providerv1.Status, error) {
	return s.status, s.err
}

type testCollector struct {
	name    string
	section EvidenceSection
	err     error
}

func (c testCollector) Name() string {
	return c.name
}

func (c testCollector) Collect(context.Context) (EvidenceSection, error) {
	return c.section, c.err
}

type testMaterialSource struct {
	material SnapshotMaterial
	err      error
}

func (s testMaterialSource) SnapshotMaterial(context.Context) (SnapshotMaterial, error) {
	return s.material, s.err
}

func TestNewMaterialPayloadSourceValidatesConfig(t *testing.T) {
	source := testMaterialSource{}
	now := func() time.Time { return time.Unix(1, 0) }

	tests := []struct {
		name    string
		cfg     MaterialPayloadSourceConfig
		wantErr error
	}{
		{
			name: "success",
			cfg: MaterialPayloadSourceConfig{
				Source:   source,
				Provider: "akash1provider",
				ChainID:  "akashnet-2",
				Now:      now,
			},
		},
		{
			name: "missing source",
			cfg: MaterialPayloadSourceConfig{
				Provider: "akash1provider",
				ChainID:  "akashnet-2",
				Now:      now,
			},
			wantErr: errMissingMaterialSource,
		},
		{
			name: "missing provider",
			cfg: MaterialPayloadSourceConfig{
				Source:  source,
				ChainID: "akashnet-2",
				Now:     now,
			},
			wantErr: errMissingProviderAddress,
		},
		{
			name: "missing chain ID",
			cfg: MaterialPayloadSourceConfig{
				Source:   source,
				Provider: "akash1provider",
				Now:      now,
			},
			wantErr: errMissingChainID,
		},
		{
			name: "missing clock",
			cfg: MaterialPayloadSourceConfig{
				Source:   source,
				Provider: "akash1provider",
				ChainID:  "akashnet-2",
			},
			wantErr: errMissingSnapshotClock,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := NewMaterialPayloadSource(test.cfg)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				require.Nil(t, payload)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, payload)
		})
	}
}

func TestMaterialPayloadSourcePayload(t *testing.T) {
	nonce := bytes.Repeat([]byte{1}, NonceSize)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	material := SnapshotMaterial{
		Cluster:           testCluster(),
		ActiveLeases:      3,
		SoftwareVersion:   "v1.2.3",
		SoftwareSignature: []byte("release-signature"),
		SoftwareIdentity:  testSoftwareIdentity(),
		EvidenceSections: []inventoryv1.SnapshotEvidenceSection{
			{
				Name:    "operator-inventory",
				Payload: []byte("operator-material"),
			},
		},
	}
	source, err := NewMaterialPayloadSource(MaterialPayloadSourceConfig{
		Source:   testMaterialSource{material: material},
		Provider: "akash1provider",
		ChainID:  "akashnet-2",
		Now:      func() time.Time { return now },
	})
	require.NoError(t, err)

	payload, err := source.Payload(context.Background(), SnapshotRequest{Nonce: nonce})
	require.NoError(t, err)

	var decoded inventoryv1.SnapshotPayload
	require.NoError(t, decoded.Unmarshal(payload))
	require.Equal(t, SnapshotPayloadSchemaVersion, decoded.SchemaVersion)
	require.Equal(t, "akash1provider", decoded.Provider)
	require.Equal(t, "akashnet-2", decoded.ChainID)
	require.Equal(t, nonce, decoded.Nonce)
	require.Equal(t, now, decoded.Timestamp)
	require.Equal(t, uint32(3), decoded.ResourceSummary.ActiveLeases)
	require.Equal(t, []inventoryv1.SnapshotEvidenceSection{
		{
			Name:    "operator-inventory",
			Payload: []byte("operator-material"),
		},
	}, decoded.EvidenceSections)
}

func TestMaterialPayloadSourcePayloadReturnsSourceError(t *testing.T) {
	expected := errors.New("source failed")
	source, err := NewMaterialPayloadSource(MaterialPayloadSourceConfig{
		Source:   testMaterialSource{err: expected},
		Provider: "akash1provider",
		ChainID:  "akashnet-2",
		Now:      func() time.Time { return time.Unix(1, 0) },
	})
	require.NoError(t, err)

	payload, err := source.Payload(context.Background(), SnapshotRequest{})
	require.ErrorIs(t, err, expected)
	require.Nil(t, payload)
}

func TestNewStatusPayloadSourceValidatesConfig(t *testing.T) {
	status := testStatusClient{}
	now := func() time.Time { return time.Unix(1, 0) }

	tests := []struct {
		name    string
		cfg     StatusPayloadSourceConfig
		wantErr error
	}{
		{
			name: "success",
			cfg: StatusPayloadSourceConfig{
				Status:   status,
				Provider: "akash1provider",
				ChainID:  "akashnet-2",
				Now:      now,
			},
		},
		{
			name: "missing status",
			cfg: StatusPayloadSourceConfig{
				Provider: "akash1provider",
				ChainID:  "akashnet-2",
				Now:      now,
			},
			wantErr: errMissingStatusClient,
		},
		{
			name: "missing provider",
			cfg: StatusPayloadSourceConfig{
				Status:  status,
				ChainID: "akashnet-2",
				Now:     now,
			},
			wantErr: errMissingProviderAddress,
		},
		{
			name: "missing chain ID",
			cfg: StatusPayloadSourceConfig{
				Status:   status,
				Provider: "akash1provider",
				Now:      now,
			},
			wantErr: errMissingChainID,
		},
		{
			name: "missing clock",
			cfg: StatusPayloadSourceConfig{
				Status:   status,
				Provider: "akash1provider",
				ChainID:  "akashnet-2",
			},
			wantErr: errMissingSnapshotClock,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := NewStatusPayloadSource(test.cfg)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				require.Nil(t, source)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, source)
		})
	}
}

func TestStatusPayloadSourcePayload(t *testing.T) {
	nonce := bytes.Repeat([]byte{1}, NonceSize)
	now := time.Date(2026, 5, 18, 20, 0, 0, 0, time.UTC)
	status := &providerv1.Status{
		Cluster: &providerv1.ClusterStatus{
			Leases: providerv1.Leases{Active: 7},
			Inventory: providerv1.Inventory{
				Cluster: testCluster(),
			},
		},
	}
	softwareIdentity := testSoftwareIdentity()

	source, err := NewStatusPayloadSource(StatusPayloadSourceConfig{
		Status:            testStatusClient{status: status},
		Provider:          "akash1provider",
		ChainID:           "akashnet-2",
		SoftwareVersion:   "v1.2.3",
		SoftwareSignature: []byte("release-signature"),
		SoftwareIdentity:  softwareIdentity,
		Now:               func() time.Time { return now },
		Collectors: []Collector{
			testCollector{
				name: "hardware",
				section: EvidenceSection{
					Payload: []byte("collector-payload"),
				},
			},
		},
	})
	require.NoError(t, err)

	payload, err := source.Payload(context.Background(), SnapshotRequest{Nonce: nonce})
	require.NoError(t, err)

	var decoded inventoryv1.SnapshotPayload
	require.NoError(t, decoded.Unmarshal(payload))
	require.Equal(t, SnapshotPayloadSchemaVersion, decoded.SchemaVersion)
	require.Equal(t, "akash1provider", decoded.Provider)
	require.Equal(t, "akashnet-2", decoded.ChainID)
	require.Equal(t, nonce, decoded.Nonce)
	require.Equal(t, now, decoded.Timestamp)
	require.Len(t, decoded.Cluster.Nodes, 2)
	require.Equal(t, "node-1", decoded.Cluster.Nodes[0].Name)
	require.Equal(t, int64(2500), decoded.Cluster.Nodes[0].Resources.CPU.Quantity.Allocatable.MilliValue())
	require.Equal(t, int64(16*1024*1024*1024), decoded.Cluster.Nodes[0].Resources.Memory.Quantity.Allocatable.Value())
	require.Len(t, decoded.Cluster.Storage, 1)
	require.Equal(t, "beta2", decoded.Cluster.Storage[0].Info.Class)
	require.Equal(t, inventoryv1.SnapshotResourceSummary{
		TotalGPUs:         2,
		TotalVCPUs:        4,
		TotalMemoryMB:     32 * 1024,
		TotalStorageMB:    110 * 1024,
		ActiveLeases:      7,
		SoftwareVersion:   "v1.2.3",
		SoftwareSignature: []byte("release-signature"),
		SoftwareIdentity:  testSoftwareIdentity(),
	}, decoded.ResourceSummary)
	require.NotSame(t, softwareIdentity, decoded.ResourceSummary.SoftwareIdentity)
	require.Len(t, decoded.EvidenceSections, 2)
	require.Equal(t, EvidenceSectionProviderStatus, decoded.EvidenceSections[0].Name)
	expectedStatusEvidence, err := MarshalDeterministic(status)
	require.NoError(t, err)
	require.Equal(t, expectedStatusEvidence, decoded.EvidenceSections[0].Payload)

	var statusEvidence providerv1.Status
	require.NoError(t, statusEvidence.Unmarshal(decoded.EvidenceSections[0].Payload))
	require.Equal(t, status.Cluster.Leases.Active, statusEvidence.Cluster.Leases.Active)
	require.Equal(t, []inventoryv1.SnapshotEvidenceSection{
		{
			Name:    "hardware",
			Payload: []byte("collector-payload"),
		},
	}, decoded.EvidenceSections[1:])

	second, err := source.Payload(context.Background(), SnapshotRequest{Nonce: nonce})
	require.NoError(t, err)
	require.Equal(t, payload, second)
}

func TestStatusPayloadSourcePayloadReturnsStatusError(t *testing.T) {
	expected := errors.New("status failed")
	source, err := NewStatusPayloadSource(StatusPayloadSourceConfig{
		Status:   testStatusClient{err: expected},
		Provider: "akash1provider",
		ChainID:  "akashnet-2",
		Now:      func() time.Time { return time.Unix(1, 0) },
	})
	require.NoError(t, err)

	payload, err := source.Payload(context.Background(), SnapshotRequest{})
	require.ErrorIs(t, err, expected)
	require.Nil(t, payload)
}

func TestStatusPayloadSourcePayloadRejectsMissingStatus(t *testing.T) {
	tests := []struct {
		name    string
		status  *providerv1.Status
		wantErr error
	}{
		{
			name:    "nil status",
			wantErr: errMissingProviderStatus,
		},
		{
			name:    "nil cluster",
			status:  &providerv1.Status{},
			wantErr: errMissingProviderCluster,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := NewStatusPayloadSource(StatusPayloadSourceConfig{
				Status:   testStatusClient{status: test.status},
				Provider: "akash1provider",
				ChainID:  "akashnet-2",
				Now:      func() time.Time { return time.Unix(1, 0) },
			})
			require.NoError(t, err)

			payload, err := source.Payload(context.Background(), SnapshotRequest{})
			require.ErrorIs(t, err, test.wantErr)
			require.Nil(t, payload)
		})
	}
}

func TestStatusPayloadSourcePayloadReturnsCollectorError(t *testing.T) {
	expected := errors.New("collector failed")
	source, err := NewStatusPayloadSource(StatusPayloadSourceConfig{
		Status: testStatusClient{
			status: &providerv1.Status{
				Cluster: &providerv1.ClusterStatus{},
			},
		},
		Provider: "akash1provider",
		ChainID:  "akashnet-2",
		Now:      func() time.Time { return time.Unix(1, 0) },
		Collectors: []Collector{
			testCollector{name: "hardware", err: expected},
		},
	})
	require.NoError(t, err)

	payload, err := source.Payload(context.Background(), SnapshotRequest{})
	require.ErrorIs(t, err, expected)
	require.Nil(t, payload)
	require.ErrorContains(t, err, "hardware")
}

func TestResourceSummaryFromClusterSaturatesUint32(t *testing.T) {
	const maxUint32 = 1<<32 - 1

	cluster := inventoryv1.Cluster{
		Nodes: inventoryv1.Nodes{
			{
				Resources: inventoryv1.NodeResources{
					CPU: inventoryv1.CPU{
						Quantity: inventoryv1.ResourcePair{
							Allocatable: resource.NewMilliQuantity((maxUint32+1)*1000, resource.DecimalSI),
						},
					},
					GPU: inventoryv1.GPU{
						Quantity: inventoryv1.ResourcePair{
							Allocatable: resource.NewQuantity(maxUint32+1, resource.DecimalSI),
						},
					},
				},
			},
		},
	}

	summary := ResourceSummaryFromCluster(cluster, 0, "", nil, nil)
	require.Equal(t, uint32(maxUint32), summary.TotalVCPUs)
	require.Equal(t, uint32(maxUint32), summary.TotalGPUs)
}

func testSoftwareIdentity() *inventoryv1.SoftwareIdentity {
	return &inventoryv1.SoftwareIdentity{
		Version:         "v1.2.3",
		ArtifactRef:     "ghcr.io/akash-network/provider:v1.2.3",
		DigestAlgorithm: "sha3-256",
		Digest:          bytes.Repeat([]byte{2}, 32),
		SignatureType:   "cosign_keyful",
		Signature:       []byte("release-signature"),
		SignatureRef:    "ghcr.io/akash-network/provider@sha256:signature",
		PublicKeyRef:    "github.com/akash-network/releases/provider.pub",
	}
}

func testCluster() inventoryv1.Cluster {
	return inventoryv1.Cluster{
		Nodes: inventoryv1.Nodes{
			{
				Name: "node-1",
				Resources: inventoryv1.NodeResources{
					CPU: inventoryv1.CPU{
						Quantity: inventoryv1.NewResourcePairMilli(2500, 2500, 0, resource.DecimalSI),
					},
					GPU: inventoryv1.GPU{
						Quantity: inventoryv1.NewResourcePair(1, 1, 0, resource.DecimalSI),
					},
					Memory: inventoryv1.Memory{
						Quantity: inventoryv1.NewResourcePair(16*1024*1024*1024, 16*1024*1024*1024, 0, resource.BinarySI),
					},
					EphemeralStorage: inventoryv1.NewResourcePair(10*1024*1024*1024, 10*1024*1024*1024, 0, resource.BinarySI),
				},
			},
			{
				Name: "node-2",
				Resources: inventoryv1.NodeResources{
					CPU: inventoryv1.CPU{
						Quantity: inventoryv1.NewResourcePairMilli(1500, 1500, 0, resource.DecimalSI),
					},
					GPU: inventoryv1.GPU{
						Quantity: inventoryv1.NewResourcePair(1, 1, 0, resource.DecimalSI),
					},
					Memory: inventoryv1.Memory{
						Quantity: inventoryv1.NewResourcePair(16*1024*1024*1024, 16*1024*1024*1024, 0, resource.BinarySI),
					},
				},
			},
		},
		Storage: inventoryv1.ClusterStorage{
			{
				Quantity: inventoryv1.NewResourcePair(100*1024*1024*1024, 100*1024*1024*1024, 0, resource.BinarySI),
				Info: inventoryv1.StorageInfo{
					Class: "beta2",
				},
			},
		},
	}
}

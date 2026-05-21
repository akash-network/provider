package hostprobe

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testSource struct {
	name          string
	hardwareClass string
	method        string
	trustDomain   string
	properties    map[string]string
	err           error
	wait          time.Duration
}

func (s testSource) Name() string {
	return s.name
}

func (s testSource) HardwareClass() string {
	return s.hardwareClass
}

func (s testSource) Method() string {
	return s.method
}

func (s testSource) TrustDomain() string {
	return s.trustDomain
}

func (s testSource) Collect(ctx context.Context, _ FileSource) (map[string]string, error) {
	if s.wait > 0 {
		timer := time.NewTimer(s.wait)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	return s.properties, s.err
}

func TestCollectorCollectBuildsEvidenceSection(t *testing.T) {
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	collector := NewCollector(Config{
		Root:          t.TempDir(),
		SourceTimeout: time.Second,
		Now:           func() time.Time { return now },
		Hostname:      func() (string, error) { return "node-a", nil },
		Sources: []Source{
			testSource{
				name:          "proc.cpuinfo",
				hardwareClass: hardwareClassCPU,
				method:        "/proc/cpuinfo",
				trustDomain:   "kernel_procfs",
				properties: map[string]string{
					"logical_cpus": "8",
					"flags":        "fpu hypervisor",
				},
			},
			testSource{
				name:          "sysfs.cpu_topology",
				hardwareClass: hardwareClassCPU,
				method:        "/sys/devices/system/cpu/online",
				trustDomain:   "kernel_sysfs",
				properties: map[string]string{
					"logical_cpus": "4",
				},
			},
		},
	})

	section, err := collector.Collect(context.Background())
	require.NoError(t, err)
	require.Equal(t, EvidenceSectionName, section.Name)

	var snapshot Snapshot
	require.NoError(t, json.Unmarshal(section.Payload, &snapshot))
	require.Equal(t, SnapshotSchemaVersion, snapshot.SchemaVersion)
	require.Equal(t, "node-a", snapshot.Host.Hostname)
	require.Equal(t, now, snapshot.CollectedAt)
	require.Len(t, snapshot.Sources, 2)
	require.Len(t, snapshot.Discrepancies, 1)
	require.Equal(t, hardwareClassCPU, snapshot.Discrepancies[0].HardwareClass)
	require.Equal(t, "logical_cpus", snapshot.Discrepancies[0].Property)
	require.True(t, snapshot.Virtualization.Detected)
	require.Contains(t, snapshot.Virtualization.Methods, "cpuinfo_hypervisor_flag")
}

func TestCollectorMarksUnavailableAndErrors(t *testing.T) {
	expected := errors.New("probe failed")
	collector := NewCollector(Config{
		SourceTimeout: time.Second,
		Sources: []Source{
			testSource{
				name:          "missing",
				hardwareClass: hardwareClassCPU,
				method:        "missing",
				trustDomain:   "test",
				err:           ErrUnavailable,
			},
			testSource{
				name:          "failed",
				hardwareClass: hardwareClassCPU,
				method:        "failed",
				trustDomain:   "test",
				err:           expected,
			},
		},
	})

	snapshot, err := collector.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, SourceStatusUnavailable, snapshot.Sources[0].Status)
	require.Equal(t, SourceStatusError, snapshot.Sources[1].Status)
}

func TestCollectorMarksTimeout(t *testing.T) {
	collector := NewCollector(Config{
		SourceTimeout: time.Nanosecond,
		Sources: []Source{
			testSource{
				name:          "slow",
				hardwareClass: hardwareClassCPU,
				method:        "slow",
				trustDomain:   "test",
				wait:          time.Hour,
			},
		},
	})

	snapshot, err := collector.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, SourceStatusTimeout, snapshot.Sources[0].Status)
}

func TestFindDiscrepancies(t *testing.T) {
	discrepancies := FindDiscrepancies([]SourceResult{
		{
			Name:          "source-a",
			HardwareClass: hardwareClassMemory,
			Status:        SourceStatusOK,
			Properties: map[string]string{
				"total_bytes": "1024",
			},
		},
		{
			Name:          "source-b",
			HardwareClass: hardwareClassMemory,
			Status:        SourceStatusOK,
			Properties: map[string]string{
				"total_bytes": "2048",
			},
		},
		{
			Name:          "source-c",
			HardwareClass: hardwareClassMemory,
			Status:        SourceStatusUnavailable,
			Properties: map[string]string{
				"total_bytes": "4096",
			},
		},
	})

	require.Equal(t, []Discrepancy{
		{
			HardwareClass: hardwareClassMemory,
			Property:      "total_bytes",
			Values: []DiscrepancyValue{
				{
					Source: "source-a",
					Value:  "1024",
				},
				{
					Source: "source-b",
					Value:  "2048",
				},
			},
		},
	}, discrepancies)
}

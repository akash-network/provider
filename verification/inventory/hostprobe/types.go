package hostprobe

import "time"

const (
	EvidenceSectionName   = "akash.inventory.hostprobe.v1"
	SnapshotSchemaVersion = uint32(1)
)

type SourceStatus string

const (
	SourceStatusOK          SourceStatus = "ok"
	SourceStatusUnavailable SourceStatus = "unavailable"
	SourceStatusError       SourceStatus = "error"
	SourceStatusTimeout     SourceStatus = "timeout"
)

type Snapshot struct {
	SchemaVersion  uint32               `json:"schema_version"`
	Host           Host                 `json:"host"`
	CollectedAt    time.Time            `json:"collected_at"`
	DurationMS     int64                `json:"duration_ms"`
	Sources        []SourceResult       `json:"sources"`
	Discrepancies  []Discrepancy        `json:"discrepancies"`
	Virtualization VirtualizationReport `json:"virtualization"`
}

type Host struct {
	Hostname      string `json:"hostname,omitempty"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	KernelRelease string `json:"kernel_release,omitempty"`
}

type SourceResult struct {
	Name          string            `json:"name"`
	HardwareClass string            `json:"hardware_class"`
	Method        string            `json:"method"`
	TrustDomain   string            `json:"trust_domain"`
	Status        SourceStatus      `json:"status"`
	Properties    map[string]string `json:"properties,omitempty"`
	Error         string            `json:"error,omitempty"`
	DurationMS    int64             `json:"duration_ms"`
}

type Discrepancy struct {
	HardwareClass string             `json:"hardware_class"`
	Property      string             `json:"property"`
	Values        []DiscrepancyValue `json:"values"`
}

type DiscrepancyValue struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

type VirtualizationReport struct {
	Detected   bool                   `json:"detected"`
	Hypervisor string                 `json:"hypervisor,omitempty"`
	Methods    []string               `json:"methods,omitempty"`
	Evidence   []VirtualizationSignal `json:"evidence,omitempty"`
}

type VirtualizationSignal struct {
	Source string `json:"source"`
	Method string `json:"method"`
	Value  string `json:"value"`
}

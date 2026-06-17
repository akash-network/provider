package tee

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConfigfsTSM implements attestation report collection via the configfs-tsm
// interface at /sys/kernel/config/tsm/report/. This is the primary attestation
// surface on NVIDIA-patched Kata guest kernels for both SNP and TDX.
//
// The TSM provider type is auto-detected by reading the "provider" attribute
// from a temporary report entry. This distinguishes AMD SEV-SNP ("sev_guest")
// from Intel TDX ("tdx_guest") VMs that both expose configfs-tsm.
type ConfigfsTSM struct {
	BasePath string // e.g., "/sys/kernel/config/tsm/report"
	teeType  string // detected at init: NameSNP or NameTDX
}

var _ Provider = (*ConfigfsTSM)(nil)

func (c *ConfigfsTSM) Name() string {
	if c.teeType != "" {
		return c.teeType
	}
	return NameSNP
}

// detectProvider creates a temporary TSM entry and reads the "provider"
// attribute to determine the underlying TEE type. Returns NameTDX for
// TDX guests, NameSNP otherwise.
func (c *ConfigfsTSM) detectProvider() {
	dir := filepath.Join(c.BasePath, fmt.Sprintf("probe-%d", time.Now().UnixNano()))
	if err := os.Mkdir(dir, 0700); err != nil {
		return
	}
	defer os.Remove(dir) //nolint:errcheck

	data, err := os.ReadFile(filepath.Join(dir, "provider"))
	if err != nil {
		return
	}

	provider := strings.TrimSpace(string(data))
	fmt.Fprintf(os.Stderr, "configfs-tsm provider: %s\n", provider)

	switch provider {
	case "tdx_guest":
		c.teeType = NameTDX
	default:
		c.teeType = NameSNP
	}
}

func (c *ConfigfsTSM) Available() bool {
	return dirExists(c.BasePath)
}

// GetQuote creates a TSM report entry, writes the nonce into inblob,
// and reads the hardware-signed report from outblob.
//
// Implementation notes from the configfs-tsm kernel interface:
//   - Each report request requires a fresh directory under the base path.
//   - inblob MUST be written with a single write (os.WriteFile does this).
//     The kernel rejects multi-write sequences.
//   - outblob sysfs size attribute lies (reports 0). os.ReadFile reads to EOF,
//     which gives the actual report (~1184 bytes for SNP).
//   - auxblob is empty on the NVIDIA-patched Kata guest kernel.
//     Certificate fetching from AMD KDS happens tenant-side.
func (c *ConfigfsTSM) GetQuote(_ context.Context, reportData [64]byte) (*QuoteResult, error) {
	dir := filepath.Join(c.BasePath, fmt.Sprintf("akash-%d", time.Now().UnixNano()))

	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, fmt.Errorf("configfs mkdir %s: %w", dir, err)
	}
	defer os.Remove(dir) //nolint:errcheck

	// Log available attributes for debugging
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		fmt.Fprintf(os.Stderr, "configfs-tsm entry attr: %s\n", e.Name())
	}

	// Single write to inblob — kernel requires single sized write
	if err := os.WriteFile(filepath.Join(dir, "inblob"), reportData[:], 0600); err != nil {
		return nil, fmt.Errorf("configfs write inblob: %w", err)
	}
	fmt.Fprintf(os.Stderr, "configfs-tsm: wrote %d bytes to inblob\n", len(reportData))

	// Read outblob — sysfs size lies, ReadFile reads to EOF
	report, err := os.ReadFile(filepath.Join(dir, "outblob"))
	if err != nil {
		return nil, fmt.Errorf("configfs read outblob: %w", err)
	}
	fmt.Fprintf(os.Stderr, "configfs-tsm: read %d bytes from outblob\n", len(report))

	// Read auxblob — expected empty on NVIDIA-patched kernel
	auxblob, _ := os.ReadFile(filepath.Join(dir, "auxblob"))

	return &QuoteResult{
		Report:    report,
		CertChain: nil, // Certificate fetching is tenant-side
		AuxBlob:   auxblob,
	}, nil
}

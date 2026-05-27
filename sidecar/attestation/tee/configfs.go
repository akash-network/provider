package tee

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ConfigfsTSM implements attestation report collection via the configfs-tsm
// interface at /sys/kernel/config/tsm/report/. This is the primary attestation
// surface on NVIDIA-patched Kata guest kernels (kata-qemu-nvidia-gpu-snp).
type ConfigfsTSM struct {
	BasePath string // e.g., "/sys/kernel/config/tsm/report"
}

var _ Provider = (*ConfigfsTSM)(nil)

func (c *ConfigfsTSM) Name() string { return NameSNP }

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

	// Single write to inblob — kernel requires single sized write
	if err := os.WriteFile(filepath.Join(dir, "inblob"), reportData[:], 0600); err != nil {
		return nil, fmt.Errorf("configfs write inblob: %w", err)
	}

	// Read outblob — sysfs size lies, ReadFile reads to EOF
	report, err := os.ReadFile(filepath.Join(dir, "outblob"))
	if err != nil {
		return nil, fmt.Errorf("configfs read outblob: %w", err)
	}

	// Read auxblob — expected empty on NVIDIA-patched kernel
	auxblob, _ := os.ReadFile(filepath.Join(dir, "auxblob"))

	return &QuoteResult{
		Report:    report,
		CertChain: nil, // Certificate fetching from AMD KDS is tenant-side
		AuxBlob:   auxblob,
	}, nil
}

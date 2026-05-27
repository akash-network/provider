package tee

import (
	"context"
	"fmt"
	"os"
)

// TEE type name constants.
const (
	NameSNP = "snp"
	NameTDX = "tdx"
)

// QuoteResult holds the raw hardware-signed attestation evidence.
type QuoteResult struct {
	Report    []byte // Raw attestation report (SNP ~1184 bytes, TDX 1024 bytes)
	CertChain []byte // Cert chain (may be empty — fetching is tenant-side)
	AuxBlob   []byte // Empty on NVIDIA-patched kernel
}

// Provider abstracts TEE-specific attestation report collection.
type Provider interface {
	// Name returns the TEE type identifier ("snp" or "tdx").
	Name() string

	// Available returns true if this TEE surface is accessible.
	Available() bool

	// GetQuote collects a hardware-signed attestation report with the
	// given report_data (typically the tenant's nonce, or a hash binding
	// the nonce to additional data like a TLS public key).
	GetQuote(ctx context.Context, reportData [64]byte) (*QuoteResult, error)
}

// Detect probes the guest environment and returns the first available TEE provider.
//
// If ATTESTATION_MOCK=true is set, returns a MockProvider that produces synthetic
// reports for local development without TEE hardware. The optional ATTESTATION_MOCK_TEE
// env var controls the mock TEE type ("snp" or "tdx", defaults to "snp").
//
// Hardware check order:
//  1. /sys/kernel/config/tsm/report/ (configfs-tsm — NVIDIA-patched Kata kernel, GPU path)
//  2. /dev/sev-guest (CPU-only AMD SEV-SNP)
//  3. /dev/tdx_guest (Intel TDX — current kernel interface)
//  4. /dev/tdx-attest (Intel TDX — legacy kernel interface)
//
// configfs-tsm is checked first because on GPU-enabled VMs with the NVIDIA-patched
// kernel, it supersedes /dev/sev-guest. The SNP and TDX device paths are mutually
// exclusive (a VM is either SEV-SNP or TDX, never both).
func Detect() (Provider, error) {
	if os.Getenv("ATTESTATION_MOCK") == "true" {
		tee := os.Getenv("ATTESTATION_MOCK_TEE")
		if tee == "" {
			tee = NameSNP
		}
		return &MockProvider{TEE: tee}, nil
	}

	configfs := &ConfigfsTSM{BasePath: "/sys/kernel/config/tsm/report"}
	if configfs.Available() {
		return configfs, nil
	}

	sevGuest := &SEVGuest{DevicePath: "/dev/sev-guest"}
	if sevGuest.Available() {
		return sevGuest, nil
	}

	tdxGuest := &TDX{DevicePath: "/dev/tdx_guest"}
	if tdxGuest.Available() {
		return tdxGuest, nil
	}

	tdxLegacy := &TDX{DevicePath: "/dev/tdx-attest"}
	if tdxLegacy.Available() {
		return tdxLegacy, nil
	}

	return nil, fmt.Errorf("no TEE attestation surface found: "+
		"tried configfs-tsm (%s), /dev/sev-guest, /dev/tdx_guest, /dev/tdx-attest "+
		"(set ATTESTATION_MOCK=true for local development)",
		configfs.BasePath)
}

// dirExists returns true if the path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

package tee

import (
	"context"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TDX implements attestation report collection via /dev/tdx_guest (or the
// legacy /dev/tdx-attest). This is the Intel TDX attestation path for
// kata-qemu-tdx and kata-qemu-nvidia-gpu-tdx runtime classes.
//
// The TDX guest device exposes the TDG.MR.REPORT instruction via ioctl,
// producing a TDREPORT that can be sent to Intel Trust Authority or
// verified independently against Intel's attestation infrastructure.
type TDX struct {
	DevicePath string // "/dev/tdx_guest" or "/dev/tdx-attest"
}

var _ Provider = (*TDX)(nil)

func (t *TDX) Name() string { return NameTDX }

func (t *TDX) Available() bool {
	_, err := os.Stat(t.DevicePath)
	return err == nil
}

// TDX ioctl constants.
// See: linux/include/uapi/linux/tdx-guest.h
const (
	// TDX_CMD_GET_REPORT0 ioctl for /dev/tdx_guest
	// Request ID: _IOWR('T', 1, struct tdx_report_req)
	tdxGetReport0Ioctl = 0xc4405401

	// TDREPORT is 1024 bytes
	tdxReportSize = 1024
)

// tdxReportReq matches struct tdx_report_req from the kernel.
// Layout: reportdata [64]byte, tdreport [1024]byte
type tdxReportReq struct {
	ReportData [64]byte
	TDReport   [tdxReportSize]byte
}

// GetQuote collects a TDX attestation report via /dev/tdx_guest ioctl.
//
// Note: This produces a TDREPORT (local attestation). For remote attestation,
// the TDREPORT is sent to a QE (Quoting Enclave) or QGS (Quote Generation
// Service) to produce a TD Quote. The sidecar returns the raw TDREPORT;
// the tenant is responsible for obtaining the full quote via the QGS.
func (t *TDX) GetQuote(_ context.Context, reportData [64]byte) (*QuoteResult, error) {
	fd, err := unix.Open(t.DevicePath, unix.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", t.DevicePath, err)
	}
	defer unix.Close(fd) //nolint:errcheck

	req := tdxReportReq{
		ReportData: reportData,
	}

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL, //nolint:staticcheck // Linux-only, runs inside Kata VM
		uintptr(fd),
		uintptr(tdxGetReport0Ioctl),
		uintptr(unsafe.Pointer(&req)),
	)
	if errno != 0 {
		return nil, fmt.Errorf("TDX_CMD_GET_REPORT0 ioctl failed: %w", errno)
	}

	// The TDREPORT is written back into req.TDReport by the kernel.
	// Extract the actual report size from the structure.
	// The first 4 bytes of the response area may contain metadata;
	// for now return the full 1024-byte TDREPORT.
	report := make([]byte, tdxReportSize)
	copy(report, req.TDReport[:])

	// Sanity check: verify the report isn't all zeros
	allZero := true
	for _, b := range report[:64] {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return nil, fmt.Errorf("TDX report appears empty (all zeros in first 64 bytes)")
	}

	return &QuoteResult{
		Report:    report,
		CertChain: nil, // TDX cert chain retrieved via Intel Trust Authority, tenant-side
		AuxBlob:   nil,
	}, nil
}

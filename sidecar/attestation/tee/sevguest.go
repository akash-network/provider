package tee

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SEVGuest implements attestation report collection via /dev/sev-guest.
// This is the CPU-only AMD SEV-SNP attestation path (kata-qemu-snp, no GPU).
//
// AEP-83 Section 6 describes this path. For GPU-enabled VMs, the NVIDIA-patched
// guest kernel exposes configfs-tsm instead — see configfs.go.
type SEVGuest struct {
	DevicePath string // e.g., "/dev/sev-guest"
}

var _ Provider = (*SEVGuest)(nil)

func (s *SEVGuest) Name() string { return NameSNP }

func (s *SEVGuest) Available() bool {
	_, err := os.Stat(s.DevicePath)
	return err == nil
}

// SNP_GET_REPORT ioctl constants for AMD SEV-SNP.
// See: linux/include/uapi/linux/sev-guest.h
const (
	// ioctl request ID for SNP_GET_REPORT on x86_64
	// _IOWR('S', 0x00, struct snp_guest_request_ioctl)
	snpGetReportIoctl = 0xc0185300

	snpReportRespSize = 4096 + 32 // sizeof(struct snp_report_resp)
)

// snpReportReq matches struct snp_report_req from the kernel.
// Layout: user_data [64]byte, vmpl uint32, padding [28]byte
type snpReportReq struct {
	UserData [64]byte
	VMPL     uint32
	_        [28]byte // padding to 96 bytes
}

// snpGuestRequestIoctl matches struct snp_guest_request_ioctl from the kernel.
// Layout: msg_version u32, pad u32, req_data u64, resp_data u64, fw_err u64
type snpGuestRequestIoctl struct {
	MsgVersion uint32
	_          uint32 // padding
	ReqData    uint64
	RespData   uint64
	FWErr      uint64
}

// GetQuote collects an SNP attestation report via /dev/sev-guest ioctl.
func (s *SEVGuest) GetQuote(_ context.Context, reportData [64]byte) (*QuoteResult, error) {
	fd, err := unix.Open(s.DevicePath, unix.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", s.DevicePath, err)
	}
	defer unix.Close(fd) //nolint:errcheck

	// Prepare request
	req := snpReportReq{
		UserData: reportData,
		VMPL:     0,
	}

	// Allocate response buffer
	resp := make([]byte, snpReportRespSize)

	// Build the ioctl envelope: struct snp_guest_request_ioctl
	// The kernel expects pointers to separate req and resp buffers.
	envelope := snpGuestRequestIoctl{
		MsgVersion: 1,
		ReqData:    uint64(uintptr(unsafe.Pointer(&req))),
		RespData:   uint64(uintptr(unsafe.Pointer(&resp[0]))),
	}

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL, //nolint:staticcheck // Linux-only, runs inside Kata VM
		uintptr(fd),
		uintptr(snpGetReportIoctl),
		uintptr(unsafe.Pointer(&envelope)),
	)
	if errno != 0 {
		return nil, fmt.Errorf("SNP_GET_REPORT ioctl failed (fw_err=0x%x): %w", envelope.FWErr, errno)
	}

	// Status is first 4 bytes of response header
	status := binary.LittleEndian.Uint32(resp[0:4])
	if status != 0 {
		return nil, fmt.Errorf("SNP_GET_REPORT firmware error: status=%d", status)
	}

	// Report size is next 4 bytes
	reportSize := binary.LittleEndian.Uint32(resp[4:8])
	if reportSize == 0 {
		return nil, fmt.Errorf("SNP_GET_REPORT returned empty report (size=0)")
	}
	if 32+reportSize > uint32(len(resp)) {
		return nil, fmt.Errorf("SNP_GET_REPORT report size %d exceeds response buffer", reportSize)
	}

	// Actual report starts after the 32-byte response header
	snpReport := make([]byte, reportSize)
	copy(snpReport, resp[32:32+reportSize])

	return &QuoteResult{
		Report:    snpReport,
		CertChain: nil, // Certificate fetching from AMD KDS is tenant-side
		AuxBlob:   nil,
	}, nil
}

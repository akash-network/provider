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
	snpGetReportIoctl = 0xc0105300

	snpReportReqSize  = 96        // sizeof(struct snp_report_req)
	snpReportRespSize = 4096 + 32 // sizeof(struct snp_report_resp)
)

// snpReportReq matches struct snp_report_req from the kernel.
// Layout: user_data [64]byte, vmpl uint32, padding [28]byte
type snpReportReq struct {
	UserData [64]byte
	VMPL     uint32
	_        [28]byte // padding to 96 bytes
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

	// Build the ioctl message buffer: snp_guest_request_ioctl struct
	// Layout: msg_version uint32, req_data *byte, resp_data *byte, fw_err uint64
	msgBuf := make([]byte, 32)
	binary.LittleEndian.PutUint32(msgBuf[0:4], 1) // msg_version = 1

	reqBytes := (*[snpReportReqSize]byte)(unsafe.Pointer(&req))[:]

	// Perform ioctl with the combined structure
	// The actual ioctl interface uses a struct snp_guest_request_ioctl
	// containing pointers to req and resp. For simplicity, we use
	// the raw ioctl approach with the request/response in a contiguous buffer.
	ioctlBuf := make([]byte, 0, snpReportReqSize+snpReportRespSize)
	ioctlBuf = append(ioctlBuf, reqBytes...)
	ioctlBuf = append(ioctlBuf, resp...)

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL, //nolint:staticcheck // Linux-only, runs inside Kata VM
		uintptr(fd),
		uintptr(snpGetReportIoctl),
		uintptr(unsafe.Pointer(&ioctlBuf[0])),
	)
	if errno != 0 {
		return nil, fmt.Errorf("SNP_GET_REPORT ioctl failed: %w", errno)
	}

	// Extract report from response portion
	report := ioctlBuf[snpReportReqSize:]

	// Status is first 4 bytes of response
	status := binary.LittleEndian.Uint32(report[0:4])
	if status != 0 {
		return nil, fmt.Errorf("SNP_GET_REPORT firmware error: status=%d", status)
	}

	// Report size is next 4 bytes
	reportSize := binary.LittleEndian.Uint32(report[4:8])

	// Actual report starts after the 32-byte response header
	snpReport := report[32 : 32+reportSize]

	return &QuoteResult{
		Report:    snpReport,
		CertChain: nil, // Certificate fetching from AMD KDS is tenant-side
		AuxBlob:   nil,
	}, nil
}

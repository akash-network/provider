package tee

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

const (
	nvmlHelperPath = "/usr/bin/nvml_attestation"
	guestMountDir  = "/mnt/guest"
)

// NvidiaGPUAttestor collects GPU attestation evidence via NVML for
// NVIDIA GPUs running in Confidential Computing mode.
//
// In Kata VMs, the GPU driver is in the guest rootfs but the NVIDIA
// Container Toolkit only bind-mounts driver files into containers that
// request GPU resources. The sidecar doesn't request GPUs, so it:
//  1. Discovers the guest rootfs device and filesystem type from /proc/self/mountinfo
//  2. Mounts the guest rootfs block device read-only at /mnt/guest
//  3. Bind-mounts /dev and /proc into the mount point
//  4. Runs nvidia-smi and nvml_attestation chrooted into /mnt/guest
//
// This ensures nvidia-smi, libnvidia-ml.so, glibc, and the kernel driver
// are all from the same guest image, no version mismatches.
// Dynamic discovery handles both erofs (modern Kata images) and ext4 (older).
type NvidiaGPUAttestor struct {
	SMIPath   string
	mountOnce sync.Once
	mountErr  error
}

// Available returns true if the guest rootfs is accessible and at least
// one GPU has Confidential Computing mode enabled.
func (n *NvidiaGPUAttestor) Available() bool {
	_, err := n.probe()
	return err == nil
}

// Probe checks for NVIDIA GPU CC support and returns a diagnostic string.
func (n *NvidiaGPUAttestor) Probe() (string, error) {
	return n.probe()
}

func (n *NvidiaGPUAttestor) probe() (string, error) {
	if err := n.ensureMount(); err != nil {
		return "", fmt.Errorf("guest rootfs: %w", err)
	}

	// Use nvidia-smi to check CC status (available in all driver versions).
	stdout, stderr, err := n.chrootExec(context.Background(), "/bin/nvidia-smi", "conf-compute", "-f")
	if err != nil {
		return "", fmt.Errorf("nvidia-smi conf-compute -f failed: %w (stderr: %s)", err, stderr)
	}

	if !strings.Contains(stdout, "ON") {
		return "", fmt.Errorf("GPU CC mode not enabled (output: %s)", strings.TrimSpace(stdout))
	}

	// Verify the NVML attestation helper can find CC-capable GPUs.
	if fileExists(guestMountDir + "/tmp/nvml_attestation") {
		stdout2, stderr2, err2 := n.chrootExecHelper(context.Background(), "probe")
		if err2 != nil {
			return "", fmt.Errorf("nvml_attestation probe failed: %w (stderr: %s)", err2, stderr2)
		}
		return fmt.Sprintf("GPU CC enabled: %s", strings.TrimSpace(stdout2)), nil
	}

	return fmt.Sprintf("GPU CC enabled: %s", strings.TrimSpace(stdout)), nil
}

// GetAllGPUAttestations collects attestation reports from ALL CC-capable GPUs.
// The binary output format from `attest-all` is:
//
//	4 bytes LE: device count
//	Per device:
//	  4 bytes LE: device index
//	  4 bytes LE: attestation report size
//	  N bytes:    attestation report
//	  4 bytes LE: CEC report size (0 if not present)
//	  N bytes:    CEC report (omitted if size is 0)
//	  4 bytes LE: cert chain size (0 if not present)
//	  M bytes:    PEM-encoded attestation cert chain (omitted if size is 0)
func (n *NvidiaGPUAttestor) GetAllGPUAttestations(ctx context.Context, nonce [64]byte) ([]GPUDeviceReport, error) {
	if err := n.ensureMount(); err != nil {
		return nil, fmt.Errorf("guest rootfs: %w", err)
	}

	nonceHex := hex.EncodeToString(nonce[:32])
	stdout, stderr, err := n.chrootExecHelper(ctx, "attest-all", nonceHex)
	if err != nil {
		return nil, fmt.Errorf("nvml_attestation attest-all failed: %w (stderr: %s)", err, stderr)
	}

	data := []byte(stdout)
	if len(data) < 4 {
		return nil, fmt.Errorf("attest-all output too short: %d bytes", len(data))
	}

	return parseMultiGPUOutput(data)
}

// parseMultiGPUOutput parses the binary output from `nvml_attestation attest-all`.
func parseMultiGPUOutput(data []byte) ([]GPUDeviceReport, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("output too short for device count header")
	}

	deviceCount := binary.LittleEndian.Uint32(data[0:4])
	if deviceCount == 0 {
		return nil, fmt.Errorf("attest-all returned 0 device reports")
	}

	// Every device requires four uint32 fields even when all variable-length
	// fields are empty. Bound the count by the payload before allocating so an
	// untrusted helper response cannot force an unreasonable allocation.
	const minimumDeviceFrameSize = 4 * 4
	if uint64(deviceCount) > uint64((len(data)-4)/minimumDeviceFrameSize) {
		return nil, fmt.Errorf("device count %d exceeds framed payload size", deviceCount)
	}

	off := 4
	reports := make([]GPUDeviceReport, 0, deviceCount)
	seenDeviceIndices := make(map[uint32]struct{}, deviceCount)

	for i := uint32(0); i < deviceCount; i++ {
		devIdx, next, err := readFrameUint32(data, off, fmt.Sprintf("device %d index", i))
		if err != nil {
			return nil, err
		}
		off = next
		if _, duplicate := seenDeviceIndices[devIdx]; duplicate {
			return nil, fmt.Errorf("duplicate device index %d", devIdx)
		}
		seenDeviceIndices[devIdx] = struct{}{}

		reportSize, next, err := readFrameUint32(data, off, fmt.Sprintf("device %d report size", i))
		if err != nil {
			return nil, err
		}
		if reportSize == 0 {
			return nil, fmt.Errorf("device %d attestation report is empty", i)
		}
		off = next
		report, next, err := readFrameBytes(data, off, reportSize, fmt.Sprintf("device %d report data", i))
		if err != nil {
			return nil, err
		}
		off = next
		attestationReport := append([]byte(nil), report...)

		cecSize, next, err := readFrameUint32(data, off, fmt.Sprintf("device %d CEC size", i))
		if err != nil {
			return nil, err
		}
		off = next
		cec, next, err := readFrameBytes(data, off, cecSize, fmt.Sprintf("device %d CEC data", i))
		if err != nil {
			return nil, err
		}
		off = next
		cecReport := append([]byte(nil), cec...)

		// The certificate-size word is mandatory, including when its value is
		// zero. Without it, the next device index is indistinguishable from a
		// certificate size in a multi-GPU payload.
		certSize, next, err := readFrameUint32(data, off, fmt.Sprintf("device %d certificate size", i))
		if err != nil {
			return nil, err
		}
		if certSize == 0 {
			return nil, fmt.Errorf("device %d certificate chain is empty", i)
		}
		off = next
		cert, next, err := readFrameBytes(data, off, certSize, fmt.Sprintf("device %d certificate data", i))
		if err != nil {
			return nil, err
		}
		off = next
		certificateChain := append([]byte(nil), cert...)

		legacyReport := make([]byte, 0, len(attestationReport)+len(cecReport)+len(certificateChain))
		legacyReport = append(legacyReport, attestationReport...)
		legacyReport = append(legacyReport, cecReport...)
		legacyReport = append(legacyReport, certificateChain...)

		reports = append(reports, GPUDeviceReport{
			DeviceIndex:       devIdx,
			Report:            legacyReport,
			AttestationReport: attestationReport,
			CECReport:         cecReport,
			CertificateChain:  certificateChain,
		})
	}

	if off != len(data) {
		return nil, fmt.Errorf("attest-all output has %d trailing bytes", len(data)-off)
	}

	return reports, nil
}

func readFrameUint32(data []byte, off int, field string) (uint32, int, error) {
	if len(data)-off < 4 {
		return 0, off, fmt.Errorf("truncated output at %s", field)
	}
	return binary.LittleEndian.Uint32(data[off : off+4]), off + 4, nil
}

func readFrameBytes(data []byte, off int, size uint32, field string) ([]byte, int, error) {
	remaining := len(data) - off
	if uint64(size) > uint64(remaining) {
		return nil, off, fmt.Errorf("truncated output at %s (need %d, have %d)", field, size, remaining)
	}
	next := off + int(size)
	return data[off:next], next, nil
}

// ensureMount prepares the chroot environment. Platform-specific setup
// (mknod, mount) is in setupGuestRootfs() defined per-platform.
// Uses sync.Once to guarantee the mount is performed exactly once,
// even under concurrent quote requests.
func (n *NvidiaGPUAttestor) ensureMount() error {
	n.mountOnce.Do(func() {
		// Check if already mounted from a previous run.
		if fileExists(guestMountDir + "/bin/nvidia-smi") {
			return
		}

		if err := setupGuestRootfs(); err != nil {
			n.mountErr = err
			return
		}

		if !fileExists(guestMountDir + "/bin/nvidia-smi") {
			n.mountErr = fmt.Errorf("nvidia-smi not found in guest rootfs at %s/bin/nvidia-smi", guestMountDir)
			return
		}

		// Place the NVML helper into a writable tmpfs directory within the
		// chroot. The guest rootfs is mounted read-only, so we create a tmpfs
		// at /mnt/guest/tmp and copy the helper there.
		if fileExists(nvmlHelperPath) {
			tmpDir := guestMountDir + "/tmp"
			if err := mountTmpfsAndCopyHelper(tmpDir, nvmlHelperPath); err != nil {
				fmt.Fprintf(os.Stderr, "nvidia-gpu: setup helper: %v\n", err)
			}
		}
	})
	return n.mountErr
}

// chrootExec runs a binary inside the guest rootfs using SysProcAttr.Chroot.
// This uses the kernel chroot syscall directly, no external `chroot` binary needed.
// The path must be relative to the chroot (e.g. "/bin/nvidia-smi").
func (n *NvidiaGPUAttestor) chrootExec(ctx context.Context, path string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Chroot: guestMountDir,
	}
	cmd.Dir = "/"

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), strings.TrimSpace(stderr.String()), err
}

// chrootExecHelper runs the nvml_attestation helper inside the chroot.
func (n *NvidiaGPUAttestor) chrootExecHelper(ctx context.Context, args ...string) (string, string, error) {
	return n.chrootExec(ctx, "/tmp/nvml_attestation", args...)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

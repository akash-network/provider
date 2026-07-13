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
	off := 4

	reports := make([]GPUDeviceReport, 0, deviceCount)

	for i := uint32(0); i < deviceCount; i++ {
		if off+4 > len(data) {
			return nil, fmt.Errorf("truncated output at device %d index", i)
		}
		devIdx := binary.LittleEndian.Uint32(data[off : off+4])
		off += 4

		if off+4 > len(data) {
			return nil, fmt.Errorf("truncated output at device %d report size", i)
		}
		reportSize := binary.LittleEndian.Uint32(data[off : off+4])
		off += 4

		if off+int(reportSize) > len(data) {
			return nil, fmt.Errorf("truncated output at device %d report data (need %d, have %d)", i, reportSize, len(data)-off)
		}
		report := make([]byte, reportSize)
		copy(report, data[off:off+int(reportSize)])
		off += int(reportSize)

		// CEC report
		if off+4 > len(data) {
			return nil, fmt.Errorf("truncated output at device %d CEC size", i)
		}
		cecSize := binary.LittleEndian.Uint32(data[off : off+4])
		off += 4

		if cecSize > 0 {
			if off+int(cecSize) > len(data) {
				return nil, fmt.Errorf("truncated output at device %d CEC data", i)
			}
			// Append CEC report to the main report
			report = append(report, data[off:off+int(cecSize)]...)
			off += int(cecSize)
		}

		// Cert chain (optional absent in older helper builds)
		if off+4 <= len(data) {
			certSize := binary.LittleEndian.Uint32(data[off : off+4])
			off += 4
			if certSize > 0 && off+int(certSize) <= len(data) {
				// Append cert chain to report tenants split on PEM marker
				report = append(report, data[off:off+int(certSize)]...)
				off += int(certSize)
			}
		}

		reports = append(reports, GPUDeviceReport{
			DeviceIndex: devIdx,
			Report:      report,
		})
	}

	if len(reports) == 0 {
		return nil, fmt.Errorf("attest-all returned 0 device reports")
	}

	return reports, nil
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

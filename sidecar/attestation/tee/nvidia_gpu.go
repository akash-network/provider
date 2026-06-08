package tee

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
//   1. Mounts the guest rootfs block device (/dev/dm-0) at /mnt/guest
//   2. Bind-mounts /dev and /proc into the mount point
//   3. Runs nvidia-smi and nvml_attestation chrooted into /mnt/guest
//
// This ensures nvidia-smi, libnvidia-ml.so, glibc, and the kernel driver
// are all from the same guest image — no version mismatches.
type NvidiaGPUAttestor struct {
	SMIPath    string
	mountReady bool
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

	// Verify the NVML attestation helper can find a CC-capable GPU.
	if fileExists(guestMountDir + "/tmp/nvml_attestation") {
		stdout2, stderr2, err2 := n.chrootExecHelper(context.Background(), "probe")
		if err2 != nil {
			return "", fmt.Errorf("nvml_attestation probe failed: %w (stderr: %s)", err2, stderr2)
		}
		return fmt.Sprintf("GPU CC enabled: %s", strings.TrimSpace(stdout2)), nil
	}

	return fmt.Sprintf("GPU CC enabled: %s", strings.TrimSpace(stdout)), nil
}

// GetGPUAttestation collects GPU attestation report and certificate chain.
func (n *NvidiaGPUAttestor) GetGPUAttestation(ctx context.Context, nonce [64]byte) ([]byte, error) {
	if err := n.ensureMount(); err != nil {
		return nil, fmt.Errorf("guest rootfs: %w", err)
	}

	nonceHex := hex.EncodeToString(nonce[:32])
	stdout, stderr, err := n.chrootExecHelper(ctx, "attest", nonceHex)
	if err != nil {
		return nil, fmt.Errorf("nvml_attestation attest failed: %w (stderr: %s)", err, stderr)
	}

	report := []byte(stdout)
	if len(report) == 0 {
		return nil, fmt.Errorf("empty attestation report")
	}

	return report, nil
}

// ensureMount prepares the chroot environment. Platform-specific setup
// (mknod, mount) is in setupGuestRootfs() defined per-platform.
func (n *NvidiaGPUAttestor) ensureMount() error {
	if n.mountReady {
		return nil
	}

	// Check if already mounted from a previous run.
	if fileExists(guestMountDir + "/bin/nvidia-smi") {
		n.mountReady = true
		return nil
	}

	if err := setupGuestRootfs(); err != nil {
		return err
	}

	if !fileExists(guestMountDir + "/bin/nvidia-smi") {
		return fmt.Errorf("nvidia-smi not found in guest rootfs at %s/bin/nvidia-smi", guestMountDir)
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

	n.mountReady = true
	return nil
}

// chrootExec runs a binary inside the guest rootfs using SysProcAttr.Chroot.
// This uses the kernel chroot syscall directly — no external `chroot` binary needed.
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

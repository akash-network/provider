package tee

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ensureConfigfsTSM mounts configfs if /sys/kernel/config/tsm doesn't exist.
// Inside kata containers, configfs may not be mounted by the runtime.
func ensureConfigfsTSM() {
	if dirExists("/sys/kernel/config/tsm") {
		return
	}
	// Kata mounts /sys read-only inside containers. Remount rw so we can
	// mount configfs underneath it (requires privileged).
	if err := unix.Mount("", "/sys", "", unix.MS_REMOUNT, ""); err != nil {
		fmt.Fprintf(os.Stderr, "setup: remount /sys rw: %v\n", err)
		return
	}
	if err := unix.Mount("none", "/sys/kernel/config", "configfs", 0, ""); err != nil {
		fmt.Fprintf(os.Stderr, "setup: mount configfs: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "setup: mounted configfs, tsm available: %v\n", dirExists("/sys/kernel/config/tsm"))
	}
}

// ensureSEVGuestDev creates /dev/sev-guest if it doesn't exist but the kernel
// has the sev-guest misc device registered. Kata containers populate a minimal
// /dev that doesn't include TEE devices.
func ensureSEVGuestDev() {
	if _, err := os.Stat("/dev/sev-guest"); err == nil {
		return
	}
	minor, ok := miscDevMinor("sev-guest")
	if !ok {
		fmt.Fprintf(os.Stderr, "setup: sev-guest not found in /proc/misc\n")
		return
	}
	fmt.Fprintf(os.Stderr, "setup: sev-guest minor=%d\n", minor)
	// misc devices use major 10
	dev := unix.Mkdev(10, minor)
	if err := unix.Mknod("/dev/sev-guest", unix.S_IFCHR|0600, int(dev)); err != nil {
		fmt.Fprintf(os.Stderr, "setup: mknod /dev/sev-guest: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "setup: created /dev/sev-guest\n")
	}
}

// ensureNvidiaDevices creates /dev/nvidiactl and /dev/nvidia0 if they don't
// exist. Inside Kata VMs with VFIO-passthrough GPUs, the guest kernel has the
// NVIDIA driver loaded but /dev is minimal. nvidia-smi needs these character
// devices to communicate with the driver.
func ensureNvidiaDevices() {
	// NVIDIA devices use major 195 (registered in /proc/devices as "nvidia-frontend").
	const nvidiaMajor = 195

	devices := []struct {
		path  string
		minor uint32
	}{
		{"/dev/nvidiactl", 255},
		{"/dev/nvidia0", 0},
	}

	for _, d := range devices {
		if _, err := os.Stat(d.path); err == nil {
			continue
		}
		dev := unix.Mkdev(nvidiaMajor, d.minor)
		if err := unix.Mknod(d.path, unix.S_IFCHR|0666, int(dev)); err != nil {
			fmt.Fprintf(os.Stderr, "setup: mknod %s: %v\n", d.path, err)
		} else {
			fmt.Fprintf(os.Stderr, "setup: created %s (major=%d, minor=%d)\n", d.path, nvidiaMajor, d.minor)
		}
	}
}

// setupGuestRootfs mounts the Kata guest rootfs at guestMountDir and
// bind-mounts /dev and /proc into it, so nvidia-smi and the NVML helper
// can run in a chroot with access to the kernel driver.
func setupGuestRootfs() error {
	const (
		guestRootDev = "/dev/dm-0"
		guestRootMaj = 253
		guestRootMin = 0
	)

	// Create /dev/dm-0 if missing (device-mapper block device).
	if _, err := os.Stat(guestRootDev); err != nil {
		dev := unix.Mkdev(guestRootMaj, guestRootMin)
		if err := unix.Mknod(guestRootDev, unix.S_IFBLK|0660, int(dev)); err != nil {
			return fmt.Errorf("mknod %s: %w", guestRootDev, err)
		}
		fmt.Fprintf(os.Stderr, "nvidia-gpu: created %s\n", guestRootDev)
	}

	// Mount guest rootfs read-only.
	if err := os.MkdirAll(guestMountDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", guestMountDir, err)
	}
	if err := unix.Mount(guestRootDev, guestMountDir, "ext4", unix.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("mount %s on %s: %w", guestRootDev, guestMountDir, err)
	}
	fmt.Fprintf(os.Stderr, "nvidia-gpu: mounted guest rootfs at %s\n", guestMountDir)

	// Bind-mount /dev and /proc so chrooted commands can access device
	// nodes (/dev/nvidiactl, /dev/nvidia0) and /proc/driver/nvidia.
	for _, mp := range []struct{ src, dst string }{
		{"/dev", guestMountDir + "/dev"},
		{"/proc", guestMountDir + "/proc"},
	} {
		if err := os.MkdirAll(mp.dst, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", mp.dst, err)
		}
		if err := unix.Mount(mp.src, mp.dst, "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind mount %s -> %s: %w", mp.src, mp.dst, err)
		}
	}

	return nil
}

// mountTmpfsAndCopyHelper mounts a tmpfs at the given directory within the
// chroot and copies the NVML helper binary into it. This is needed because
// the guest rootfs is mounted read-only.
func mountTmpfsAndCopyHelper(tmpDir, helperSrc string) error {
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", tmpDir, err)
	}
	if err := unix.Mount("tmpfs", tmpDir, "tmpfs", 0, "size=10m"); err != nil {
		return fmt.Errorf("mount tmpfs on %s: %w", tmpDir, err)
	}
	data, err := os.ReadFile(helperSrc)
	if err != nil {
		return fmt.Errorf("read %s: %w", helperSrc, err)
	}
	dst := tmpDir + "/nvml_attestation"
	if err := os.WriteFile(dst, data, 0755); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	fmt.Fprintf(os.Stderr, "nvidia-gpu: installed helper at %s\n", dst)
	return nil
}

// miscDevMinor reads /proc/misc to find the minor number for a named device.
func miscDevMinor(name string) (uint32, bool) {
	f, err := os.Open("/proc/misc")
	if err != nil {
		return 0, false
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == name {
			n, err := strconv.ParseUint(fields[0], 10, 32)
			if err != nil {
				return 0, false
			}
			return uint32(n), true
		}
	}
	return 0, false
}

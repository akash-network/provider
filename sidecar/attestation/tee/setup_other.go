//go:build !linux

package tee

// ensureConfigfsTSM is a no-op on non-Linux platforms.
func ensureConfigfsTSM() {}

// ensureSEVGuestDev is a no-op on non-Linux platforms.
func ensureSEVGuestDev() {}

// ensureNvidiaDevices is a no-op on non-Linux platforms.
func ensureNvidiaDevices() {}

// setupGuestRootfs is a no-op on non-Linux platforms.
func setupGuestRootfs() error { return nil }

// mountTmpfsAndCopyHelper is a no-op on non-Linux platforms.
func mountTmpfsAndCopyHelper(_, _ string) error { return nil }

//go:build darwin

package sysinfo

import "golang.org/x/sys/unix"

// TotalMemoryGiB returns the installed memory in GiB on macOS.
func TotalMemoryGiB() int {
	size, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}

	//nolint:gosec // G115: the GiB value fits in an int on macOS.
	return int(size / (kiB * kiB * kiB))
}

// AvailableMemoryMiB reports ok=false on macOS: the free page count sysctl
// exposes leaves out the inactive and compressible memory macOS reclaims on
// demand, so it would understate what is available.
func AvailableMemoryMiB() (uint32, bool) {
	return 0, false
}

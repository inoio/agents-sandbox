//go:build darwin

package sysinfo

import (
	"strconv"
	"strings"
	"syscall"
)

// TotalMemoryGiB returns the installed memory in GiB on macOS.
// Uses syscall.Sysctl("hw.memsize") because syscall.SysctlUint64 is not
// available when cross-compiling from non-darwin hosts (Go toolchain limitation).
func TotalMemoryGiB() int {
	result, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}
	size, err := strconv.ParseUint(strings.TrimSpace(result), 10, 64)
	if err != nil {
		return 0
	}
	return int(size / (kiB * kiB * kiB))
}

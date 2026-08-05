//go:build darwin

package sysinfo

import (
	"strconv"
	"strings"
	"syscall"
)

func TotalMemoryGiB() int {
	result, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}
	size, err := strconv.ParseUint(strings.TrimSpace(result), 10, 64)
	if err != nil {
		return 0
	}
	return int(size / (1024 * 1024 * 1024))
}

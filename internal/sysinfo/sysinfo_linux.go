//go:build linux

package sysinfo

import (
	"os"
)

const totalMemoryKiB = 1024 * 1024

func TotalMemoryGiB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	totalKB, ok := parseMemInfo(data)
	if !ok {
		return 0
	}
	return totalKB / totalMemoryKiB
}

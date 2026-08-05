//go:build linux

package sysinfo

import (
	"os"
)

func TotalMemoryGiB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	totalKB, ok := parseMemInfo(data)
	if !ok {
		return 0
	}
	return totalKB / (kiB * kiB)
}

//go:build linux

package sysinfo

import (
	"os"
)

// procMeminfoPath is overridable so tests can exercise the read and parse
// error paths without a real /proc/meminfo.
var procMeminfoPath = "/proc/meminfo"

func TotalMemoryGiB() int {
	data, err := os.ReadFile(procMeminfoPath)
	if err != nil {
		return 0
	}
	totalKB, ok := parseMemInfo(data)
	if !ok {
		return 0
	}
	return totalKB / (kiB * kiB)
}

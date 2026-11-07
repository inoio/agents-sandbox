//go:build linux

package sysinfo

import (
	"os"
)

var procMeminfoPath = "/proc/meminfo"

var readMeminfo = os.ReadFile

func TotalMemoryGiB() int {
	data, err := readMeminfo(procMeminfoPath)
	if err != nil {
		// os.ReadFile can return the bytes read before the error, so a
		// partial read must be discarded, not parsed.
		return 0
	}
	totalKB, ok := parseMemInfo(data, memTotalField)
	// gomutants:disable-next-line BRANCH_IF reason="parseMemInfo returns 0 whenever ok is false"
	if !ok {
		return 0
	}
	return totalKB / (kiB * kiB)
}

// AvailableMemoryMiB returns the memory the host can still hand out without
// swapping (MemAvailable), or ok=false when it cannot be read.
func AvailableMemoryMiB() (uint32, bool) {
	data, err := readMeminfo(procMeminfoPath)
	if err != nil {
		return 0, false
	}
	availableKB, ok := parseMemInfo(data, memAvailableField)
	//nolint:gosec // G115: host memory in MiB fits uint32 up to 4 PiB
	return uint32(availableKB / kiB), ok
}

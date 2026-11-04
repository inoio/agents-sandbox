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
	totalKB, ok := parseMemInfo(data)
	// gomutants:disable-next-line BRANCH_IF reason="parseMemInfo returns 0 whenever ok is false"
	if !ok {
		return 0
	}
	return totalKB / (kiB * kiB)
}

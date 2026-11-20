//go:build windows

package sysinfo

import (
	"math"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// FreeDiskMiB returns the space an unprivileged user can still write on the
// filesystem holding path, measured at path or its nearest existing ancestor,
// or ok=false when no ancestor can be stat'ed.
func FreeDiskMiB(path string) (uint32, bool) {
	current := filepath.Clean(path)
	for {
		var freeToCaller, totalBytes, totalFree uint64
		ptr, err := windows.UTF16PtrFromString(current)
		if err == nil &&
			windows.GetDiskFreeSpaceEx(ptr, &freeToCaller, &totalBytes, &totalFree) == nil {
			return uint32(min(freeToCaller>>20, uint64(math.MaxUint32))), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return 0, false
		}
		current = parent
	}
}

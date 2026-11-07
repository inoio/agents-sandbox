package sysinfo

import (
	"math"
	"math/bits"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// statfs is the filesystem-stat seam, substituted by tests to exercise a path
// no filesystem reports on.
var statfs = unix.Statfs

// FreeDiskMiB returns the space an unprivileged user can still write on the
// filesystem holding path, measured at path or its nearest existing ancestor,
// or ok=false when no ancestor can be stat'ed.
func FreeDiskMiB(path string) (uint32, bool) {
	current := filepath.Clean(path)
	for {
		var stat unix.Statfs_t
		if statfs(current, &stat) == nil {
			//nolint:gosec // G115: the block size is positive
			overflow, freeBytes := bits.Mul64(stat.Bavail, uint64(stat.Bsize))
			if overflow != 0 {
				return math.MaxUint32, true
			}
			return uint32(min(freeBytes>>20, math.MaxUint32)), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return 0, false
		}
		current = parent
	}
}

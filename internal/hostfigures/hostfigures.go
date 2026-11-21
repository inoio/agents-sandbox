// Package hostfigures holds the host figures the launcher derives run defaults
// from: installed RAM, the logical CPU count, and the memory the host can still
// hand out. They sit behind seams so a test binary can pin them to a fixed
// machine instead of the one it runs on, and so the hermetic sentinel pass can
// make any test that still reads the real host figures fail. Production reads
// the machine. See specs/testing/test-isolation.md.
package hostfigures

import "github.com/inoio/agents-sandbox/internal/sysinfo"

var (
	totalMemoryGiB     = sysinfo.TotalMemoryGiB
	numCPUs            = sysinfo.NumCPUs
	availableMemoryMiB = sysinfo.AvailableMemoryMiB
)

// TotalMemoryGiB is the host's installed RAM in whole GiB.
func TotalMemoryGiB() int { return totalMemoryGiB() }

// NumCPUs is the host's logical CPU count.
func NumCPUs() uint8 { return numCPUs() }

// AvailableMemoryMiB is the memory the host can still hand out without swapping,
// and whether that figure is known.
func AvailableMemoryMiB() (uint32, bool) { return availableMemoryMiB() }

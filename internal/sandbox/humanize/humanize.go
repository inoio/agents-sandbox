package humanize

import (
	"fmt"
	"strconv"
)

// FormatBytes renders a byte count in a human-readable form (e.g. 1.2G),
// using 1024-based units. Values below 1 KiB render as a plain byte count.
func FormatBytes(bytes uint64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
		tib = 1024 * gib
	)
	switch {
	case bytes >= tib:
		return format(bytes, tib, "T")
	case bytes >= gib:
		return format(bytes, gib, "G")
	case bytes >= mib:
		return format(bytes, mib, "M")
	case bytes >= kib:
		return format(bytes, kib, "K")
	default:
		return strconv.FormatUint(bytes, 10)
	}
}

func format(bytes, unit uint64, suffix string) string {
	return fmt.Sprintf("%.1f%s", float64(bytes)/float64(unit), suffix)
}

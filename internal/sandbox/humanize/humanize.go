package humanize

import (
	"fmt"
	"math"
	"time"
)

// FormatBytes renders a byte count with binary units (base 1024), one decimal
// place, and a trailing ".0" trimmed. For example 1536 -> "1.5 KiB" and
// 2147483648 -> "2 GiB". Values below 1 KiB render with a "B" suffix.
func FormatBytes(bytes uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(bytes)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	mantissa := math.Round(value*10) / 10
	if mantissa == math.Trunc(mantissa) {
		return fmt.Sprintf("%.0f %s", mantissa, units[unit])
	}
	return fmt.Sprintf("%.1f %s", mantissa, units[unit])
}

// FormatTimestamp renders a timestamp as YYYY-MM-DD HH:MM:SS in the time's own
// location, or an empty string for the zero value.
func FormatTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

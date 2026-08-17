package image

import (
	"fmt"
	"math"
	"time"
)

const unknownSize = "unknown"

// FormatSize renders a byte count with binary units (base 1024), one decimal
// place, and a trailing ".0" trimmed. A nil count renders as "unknown".
func FormatSize(bytes *int64) string {
	if bytes == nil {
		return unknownSize
	}
	b := *bytes
	if b == 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(b)
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

// FormatImageTime renders a timestamp as YYYY-MM-DD HH:MM:SS in the time's own
// location, or an empty string for the zero value.
func FormatImageTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

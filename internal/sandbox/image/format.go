package image

import (
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/humanize"
)

// FormatImageTime renders a timestamp as YYYY-MM-DD HH:MM:SS in the time's own
// location, or an empty string for the zero value.
func FormatImageTime(t time.Time) string {
	return humanize.FormatTimestamp(t)
}

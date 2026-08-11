package reprovision

import (
	"strconv"
	"strings"
)

// reconfigChange describes one changed setting for prompt display. Values are
// shown for simple sizes/counts; env/secrets/config carry labels only (old/new empty).
type reconfigChange struct {
	label string
	old   string
	new   string
}

func sizeChange(label string, old, newSize uint32, oldRaw, newRaw string) reconfigChange {
	return reconfigChange{
		label: label,
		old:   formatSizeSpec(old, oldRaw),
		new:   formatSizeSpec(newSize, newRaw),
	}
}

// parseSizeSpec parses a memory spec ("" means runtime default -> not parsed).
func parseSizeSpec(spec string) (uint32, bool) {
	if spec == "" {
		return 0, false
	}
	spec = strings.TrimSpace(spec)
	multiplier := uint32(1)
	last := spec[len(spec)-1]
	rest := spec
	switch last {
	case 'g', 'G':
		multiplier = 1024
		rest = spec[:len(spec)-1]
	case 'm', 'M':
		multiplier = 1
		rest = spec[:len(spec)-1]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return uint32(n) * multiplier, true //nolint:gosec // G115: bounded spec size
}

// formatSizeSpec returns the raw user spec verbatim when it matches valueMiB,
// else the normalized "<valueMiB>M" form.
func formatSizeSpec(valueMiB uint32, raw string) string {
	if v, ok := parseSizeSpec(raw); ok && v == valueMiB {
		return raw
	}
	return strconv.FormatUint(uint64(valueMiB), 10) + "M"
}

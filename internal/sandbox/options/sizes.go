package options

import (
	"strconv"
	"strings"
)

const (
	DefaultMemoryMiB  uint32 = 4096
	DefaultTmpSizeMiB uint32 = 2048
	MaxSandboxNameLen        = 128
	mibPerGib                = 1024
)

// ParseMemoryGiB converts GiB to MiB.
func ParseMemoryGiB(gib uint32) uint32 {
	return gib * mibPerGib
}

// ParseMemoryOK parses a size spec like "4G", "512M", or "2048" and reports
// whether it parsed. Empty or unparseable spec returns ok=false.
func ParseMemoryOK(spec string) (uint32, bool) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return 0, false
	}
	multiplier := uint32(1)
	rest := trimmed
	switch last := trimmed[len(trimmed)-1]; last {
	case 'g', 'G':
		multiplier = 1024
		rest = trimmed[:len(trimmed)-1]
	case 'm', 'M':
		multiplier = 1
		rest = trimmed[:len(trimmed)-1]
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false
	}
	if multiplier > 1 && uint64(n)*uint64(multiplier) > uint64(^uint32(0)) {
		return 0, false
	}
	return uint32(n) * multiplier, true //nolint:gosec // G115: bounded spec size
}

// ParseMemory parses a memory size specification like "4G", "512M", or "2048".
// Returns the value in MiB.
func ParseMemory(spec string) uint32 {
	if v, ok := ParseMemoryOK(spec); ok {
		return v
	}
	return DefaultMemoryMiB
}

// ResolveTmpSizeMiB returns the tmpfs size in MiB. An empty spec uses the default.
func ResolveTmpSizeMiB(spec string) uint32 {
	if v, ok := ParseMemoryOK(spec); ok {
		return v
	}
	return DefaultTmpSizeMiB
}

package options

import (
	"strconv"
	"strings"
)

const (
	DefaultMemoryMiB  = 4096
	DefaultTmpSizeMiB = 2048
	MaxSandboxNameLen = 128
	mibPerGib         = 1024
)

// ParseMemoryGiB converts GiB to MiB.
func ParseMemoryGiB(gib uint32) uint32 {
	return gib * mibPerGib
}

// ParseMemory parses a memory size specification like "4G", "512M", or "2048".
// Returns the value in MiB.
func ParseMemory(spec string) uint32 {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return DefaultMemoryMiB
	}
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
		return DefaultMemoryMiB
	}
	return uint32(n) * multiplier //nolint:gosec // G115: n is from Atoi on a memory spec, bounded by realistic values
}

// ResolveTmpSizeMiB returns the tmpfs size in MiB. An empty spec uses the default.
func ResolveTmpSizeMiB(spec string) uint32 {
	if spec == "" {
		return DefaultTmpSizeMiB
	}
	return ParseMemory(spec)
}

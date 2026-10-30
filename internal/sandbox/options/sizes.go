package options

import (
	"math"
	"strconv"
	"strings"
)

const (
	DefaultMemoryMiB         uint32 = 4096
	DefaultTmpSizeMiB        uint32 = 2048
	DefaultWorkspaceQuotaMiB uint32 = 16 * 1024
	MaxSandboxNameLen               = 128
	mibPerGib                       = 1024
	// maxGiBCount is the largest count a "G" suffix can scale into uint32 MiB.
	maxGiBCount = math.MaxUint32 / mibPerGib
)

// ParseMemoryGiB converts GiB to MiB.
func ParseMemoryGiB(gib uint32) uint32 {
	return gib * mibPerGib
}

// ParseMemoryOK parses a size spec like "4G", "512M", or "2048" and reports
// whether it parsed. Empty or unparseable spec returns ok=false, as does a spec
// whose value does not fit in uint32 MiB.
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
	// Parsing at the destination width rejects a count the uint32 MiB result
	// cannot hold, rather than letting strconv.Atoi's architecture-dependent int
	// wrap it. The "G" form additionally needs a count small enough that scaling
	// it by mibPerGib stays in range.
	count, err := strconv.ParseUint(rest, 10, 32)
	if err != nil {
		return 0, false
	}
	// gomutants:disable-next-line INTEGER_INCREMENT reason="multiplier is only ever the literal 1 or 1024"
	if multiplier > 1 && count > maxGiBCount {
		return 0, false
	}
	return uint32(count) * multiplier, true
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

// ResolveWorkspaceQuotaMiB returns the workspace bind mount quota in MiB.
// An empty spec uses the default.
func ResolveWorkspaceQuotaMiB(spec string) uint32 {
	if v, ok := ParseMemoryOK(spec); ok {
		return v
	}
	return DefaultWorkspaceQuotaMiB
}

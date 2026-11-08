package options

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	DefaultMemoryMiB         uint32 = 4096
	DefaultTmpSizeCapMiB     uint32 = 2048
	DefaultWorkspaceQuotaMiB uint32 = 16 * 1024
	// MinMemoryMiB is a sanity floor for the configured memory, not a runtime
	// requirement: the microsandbox SDK only requires memory > 0. It catches
	// typos and unit mistakes, and keeps the memory-derived tmp default usable
	// (memory/2 >= 64 at the floor).
	MinMemoryMiB uint32 = 128
)

const (
	MaxSandboxNameLen = 128
	mibPerGib         = 1024
	// maxGiBCount is the largest count a "G" suffix can scale into uint32 MiB.
	maxGiBCount = math.MaxUint32 / mibPerGib
)

// DefaultRootDiskMiB is the managed root disk the microsandbox runtime gives a
// VM created without a disk size.
const DefaultRootDiskMiB uint32 = 4096

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

// ResolveTmpSizeMiB returns the tmpfs size in MiB: the parsed spec when set,
// otherwise min(DefaultTmpSizeCapMiB, memoryMiB/2). memoryMiB is the resolved
// memory limit the RAM-backed tmpfs shares and is always > 0 (validation rejects
// a zero memory).
func ResolveTmpSizeMiB(spec string, memoryMiB uint32) uint32 {
	if v, ok := ParseMemoryOK(spec); ok {
		return v
	}
	return min(memoryMiB/2, DefaultTmpSizeCapMiB)
}

// ValidateSizes checks the VM size settings against their contract: memory at
// or above MinMemoryMiB, an explicit tmp-size positive and strictly below
// memory, and an explicit disk-size and workspace-quota positive. An unset spec
// is the caller's valid default and is not range-checked here; a host-RAM clamp
// of memory is deliberately absent (vm.bootMemoryMiB clamps at boot, it does not
// reject). It is a pure function of the raw specs, so a bad flag and a bad
// config value are caught the same way.
func ValidateSizes(memorySpec, tmpSpec, diskSizeSpec, workspaceQuotaSpec string) error {
	memoryMiB, err := validateMemorySize(memorySpec)
	if err != nil {
		return err
	}
	if err := validateTmpSize(tmpSpec, memoryMiB, memorySpec); err != nil {
		return err
	}
	if err := validatePositiveSize("disk-size", diskSizeSpec, "16G, 512M, or 4096"); err != nil {
		return err
	}
	return validatePositiveSize("workspace-quota", workspaceQuotaSpec, "16G, 512M, or 4096")
}

// validateMemorySize resolves the memory spec, defaulting an empty one and
// enforcing the MinMemoryMiB floor.
func validateMemorySize(spec string) (uint32, error) {
	if spec == "" {
		return DefaultMemoryMiB, nil
	}
	parsed, ok := ParseMemoryOK(spec)
	if !ok {
		// gomutants:disable-next-line INTEGER_INCREMENT reason="the value accompanies a non-nil error, which the caller returns before using it"
		return 0, fmt.Errorf("invalid --memory %q: expected a size like 8G, 512M, or 4096", spec)
	}
	if parsed < MinMemoryMiB {
		// gomutants:disable-next-line INTEGER_INCREMENT reason="the value accompanies a non-nil error, which the caller returns before using it"
		return 0, fmt.Errorf("--memory %s is below the %dM minimum", spec, MinMemoryMiB)
	}
	return parsed, nil
}

// validateTmpSize rejects a non-positive or at-or-above-memory explicit tmp
// size. An unset spec (the memory-derived default) is valid by construction.
func validateTmpSize(spec string, memoryMiB uint32, memorySpec string) error {
	if spec == "" {
		return nil
	}
	tmpMiB, ok := ParseMemoryOK(spec)
	if !ok {
		return fmt.Errorf("invalid --tmp-size %q: expected a size like 2G, 512M, or 2048", spec)
	}
	if tmpMiB == 0 {
		return fmt.Errorf("--tmp-size %s must be greater than 0", spec)
	}
	if tmpMiB >= memoryMiB {
		return fmt.Errorf("--tmp-size %s must be below memory %s", spec, memoryDisplaySpec(memorySpec))
	}
	return nil
}

// validatePositiveSize rejects an explicit non-positive value for a size key
// whose only contract is "> 0 when set".
func validatePositiveSize(key, spec, example string) error {
	if spec == "" {
		return nil
	}
	mib, ok := ParseMemoryOK(spec)
	if !ok {
		return fmt.Errorf("invalid --%s %q: expected a size like %s", key, spec, example)
	}
	if mib == 0 {
		return fmt.Errorf("--%s %s must be greater than 0", key, spec)
	}
	return nil
}

// memoryDisplaySpec names the memory bound in an error: the raw spec when set,
// else the default rendered as "<MiB>M".
func memoryDisplaySpec(spec string) string {
	if spec != "" {
		return spec
	}
	return strconv.FormatUint(uint64(DefaultMemoryMiB), 10) + "M"
}

// ResolveWorkspaceQuotaMiB returns the workspace bind mount quota in MiB.
// An empty spec uses the default.
func ResolveWorkspaceQuotaMiB(spec string) uint32 {
	if v, ok := ParseMemoryOK(spec); ok {
		return v
	}
	return DefaultWorkspaceQuotaMiB
}

// ResolveDiskSizeMiB returns the root disk size in MiB. An empty spec is the
// runtime's default managed root.
func ResolveDiskSizeMiB(spec string) uint32 {
	if v, ok := ParseMemoryOK(spec); ok {
		return v
	}
	return DefaultRootDiskMiB
}

package pressure

import (
	"strconv"
	"strings"
)

// The disk limits a project VM runs into.
const (
	KindRootDisk       Kind = "root-disk"
	KindWorkspaceQuota Kind = "workspace-quota"
	KindTmp            Kind = "tmp"
)

// EvidenceDiskUsage is a disk limit's usage as the in-guest df reports it.
const EvidenceDiskUsage Evidence = "disk-usage"

// diskTripCapKiB caps a mount's trip level: a large mount trips at 2 GiB free.
const diskTripCapKiB = 2 * 1024 * 1024

// mountKinds maps the probed mount points to the limit each one measures.
var mountKinds = map[string]Kind{
	"/":          KindRootDisk,
	"/workspace": KindWorkspaceQuota,
	"/tmp":       KindTmp,
}

// DiskUsage is one mount's size, use and free space in KiB. For /workspace the
// size is the guest-write budget: existing host content counts as used.
type DiskUsage struct {
	SizeKiB  uint64
	UsedKiB  uint64
	AvailKiB uint64
}

// DiskProbeCommand is the in-guest command that measures the three disk
// limits: POSIX df output, one line per mount, in KiB.
func DiskProbeCommand() (string, []string) {
	return "df", []string{"-Pk", "/", "/workspace", "/tmp"}
}

// ParseDiskUsage reads the probe's output into the usage of each limited mount.
// A line that does not parse drops only its own mount.
func ParseDiskUsage(output string) map[Kind]DiskUsage {
	usages := make(map[Kind]DiskUsage, len(mountKinds))
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		kind, limited := mountKinds[fields[5]]
		size, sizeOK := parseKiB(fields[1])
		used, usedOK := parseKiB(fields[2])
		avail, availOK := parseKiB(fields[3])
		if !limited || !sizeOK || !usedOK || !availOK {
			continue
		}
		usages[kind] = DiskUsage{SizeKiB: size, UsedKiB: used, AvailKiB: avail}
	}
	return usages
}

// parseKiB parses one of df's KiB columns.
func parseKiB(column string) (uint64, bool) {
	kib, err := strconv.ParseUint(column, 10, 0)
	return kib, err == nil
}

// diskTripLevelKiB is the free space below which a mount trips: 15 % of its
// size, at most 2 GiB, so small mounts trip at 85 % used and large ones at
// 2 GiB free (specs/resource-limit-proposal.md §1, threshold).
func diskTripLevelKiB(sizeKiB uint64) uint64 {
	return min(sizeKiB*15/100, diskTripCapKiB)
}

// DiskTripped reports whether a mount is short of space.
func DiskTripped(u DiskUsage) bool {
	return u.AvailKiB < diskTripLevelKiB(u.SizeKiB)
}

// DiskRecovered reports whether a mount's free space is back above twice its
// trip level, so an answered offer may be made again on a future trip.
func DiskRecovered(u DiskUsage) bool {
	return u.AvailKiB > 2*diskTripLevelKiB(u.SizeKiB)
}

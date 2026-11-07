package pressure

import (
	"reflect"
	"testing"
)

// TestParseDiskUsageReadsTheLimitedMounts pins the parse of the probe's
// `df -Pk` output in both the busybox and the coreutils format: the root, the
// /workspace budget and /tmp, keyed by kind, and nothing else.
func TestParseDiskUsageReadsTheLimitedMounts(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name: "busybox",
			output: "Filesystem           1024-blocks    Used Available Capacity Mounted on\n" +
				"overlay                 961432        60    944988   0% /\n" +
				"workspace_c52ddf65      131072    102400     28672  78% /workspace\n" +
				"tmpfs                    65536     65536         0 100% /tmp\n",
		},
		{
			name: "coreutils",
			output: "Filesystem         1024-blocks   Used Available Capacity Mounted on\n" +
				"overlay                 961432     60    944988       1% /\n" +
				"workspace_c52ddf65      131072 102400     28672      79% /workspace\n" +
				"tmpfs                    65536  65536         0     100% /tmp\n" +
				"devtmpfs                 10240      0     10240       0% /dev\n",
		},
	}
	want := []struct {
		kind  Kind
		usage DiskUsage
	}{
		{kind: KindRootDisk, usage: DiskUsage{SizeKiB: 961432, UsedKiB: 60, AvailKiB: 944988}},
		{kind: KindWorkspaceQuota, usage: DiskUsage{SizeKiB: 131072, UsedKiB: 102400, AvailKiB: 28672}},
		{kind: KindTmp, usage: DiskUsage{SizeKiB: 65536, UsedKiB: 65536, AvailKiB: 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDiskUsage(tc.output)
			if len(got) != len(want) {
				t.Errorf("ParseDiskUsage = %+v, want exactly the %d limited mounts", got, len(want))
			}
			for _, w := range want {
				if got[w.kind] != w.usage {
					t.Errorf("%s = %+v, want %+v", w.kind, got[w.kind], w.usage)
				}
			}
		})
	}
}

// TestParseDiskUsageDropsOnlyMalformedLines pins that a line that does not
// parse (too few fields, a non-number in any KiB column) costs only its own
// mount, wherever it stands, and that missing mounts are simply absent.
func TestParseDiskUsageDropsOnlyMalformedLines(t *testing.T) {
	output := "\n" +
		"tmpfs 65536 0 65536 0%\n" +
		"Filesystem 1024-blocks Used Available Capacity Mounted on\n" +
		"overlay 961432 60 944988 0% /\n" +
		"workspace_x 131072 lots 28672 78% /workspace\n" +
		"workspace_x big 1 28672 78% /workspace\n" +
		"workspace_x 131072 1 free 78% /workspace\n" +
		"tmpfs 65536 65536\n"
	got := ParseDiskUsage(output)
	if len(got) != 1 || got[KindRootDisk] != (DiskUsage{SizeKiB: 961432, UsedKiB: 60, AvailKiB: 944988}) {
		t.Errorf("ParseDiskUsage = %+v, want only the root disk", got)
	}
	if got := ParseDiskUsage(""); len(got) != 0 {
		t.Errorf("ParseDiskUsage(\"\") = %+v, want no mounts", got)
	}
}

// TestDiskProbeCommand pins the probe: POSIX one-line-per-mount output in KiB
// for exactly the three limited mounts.
func TestDiskProbeCommand(t *testing.T) {
	command, args := DiskProbeCommand()
	if command != "df" || !reflect.DeepEqual(args, []string{"-Pk", "/", "/workspace", "/tmp"}) {
		t.Errorf("DiskProbeCommand = %q %q, want df -Pk / /workspace /tmp", command, args)
	}
}

// TestDiskThreshold pins the size-adaptive threshold: a mount trips when its
// free space drops below min(15 % of its size, 2 GiB) and re-arms only once
// free space is back above twice that. A 2 GiB /tmp trips at 85 % used, a
// 64 GiB root at 2 GiB free; an unknown size never trips or re-arms.
func TestDiskThreshold(t *testing.T) {
	const (
		twoGiBKiB    = 2 * 1024 * 1024
		sixtyFourGiB = 64 * 1024 * 1024
		smallTrip    = twoGiBKiB * 15 / 100 // 314572 KiB
	)
	tests := []struct {
		name          string
		usage         DiskUsage
		wantTripped   bool
		wantRecovered bool
	}{
		{
			name:        "small mount just below its trip level",
			usage:       DiskUsage{SizeKiB: twoGiBKiB, AvailKiB: smallTrip - 1},
			wantTripped: true,
		},
		{name: "small mount at its trip level", usage: DiskUsage{SizeKiB: twoGiBKiB, AvailKiB: smallTrip}},
		{name: "small mount at twice its trip level", usage: DiskUsage{SizeKiB: twoGiBKiB, AvailKiB: 2 * smallTrip}},
		{
			name:          "small mount above twice its trip level",
			usage:         DiskUsage{SizeKiB: twoGiBKiB, AvailKiB: 2*smallTrip + 1},
			wantRecovered: true,
		},
		{
			name:        "large mount just below 2 GiB free",
			usage:       DiskUsage{SizeKiB: sixtyFourGiB, AvailKiB: twoGiBKiB - 1},
			wantTripped: true,
		},
		{name: "large mount at 2 GiB free", usage: DiskUsage{SizeKiB: sixtyFourGiB, AvailKiB: twoGiBKiB}},
		{
			name:          "large mount above 4 GiB free",
			usage:         DiskUsage{SizeKiB: sixtyFourGiB, AvailKiB: 2*twoGiBKiB + 1},
			wantRecovered: true,
		},
		{name: "unknown size", usage: DiskUsage{SizeKiB: 0, AvailKiB: 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DiskTripped(tc.usage); got != tc.wantTripped {
				t.Errorf("DiskTripped(%+v) = %v, want %v", tc.usage, got, tc.wantTripped)
			}
			if got := DiskRecovered(tc.usage); got != tc.wantRecovered {
				t.Errorf("DiskRecovered(%+v) = %v, want %v", tc.usage, got, tc.wantRecovered)
			}
		})
	}
}

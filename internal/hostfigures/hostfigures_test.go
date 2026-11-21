package hostfigures

import (
	"reflect"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sysinfo"
)

// The seam defaults as declared, captured at package init before any test
// override, so a test can pin the production wiring.
var (
	defaultTotalMemoryGiB     = totalMemoryGiB
	defaultNumCPUs            = numCPUs
	defaultAvailableMemoryMiB = availableMemoryMiB
)

// TestDefaultsAreSysinfo pins that each seam's default is the production sysinfo
// read: it compares the function identity, so the assertion itself reads no host
// figure.
func TestDefaultsAreSysinfo(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want any
	}{
		{name: "TotalMemoryGiB", got: defaultTotalMemoryGiB, want: sysinfo.TotalMemoryGiB},
		{name: "NumCPUs", got: defaultNumCPUs, want: sysinfo.NumCPUs},
		{name: "AvailableMemoryMiB", got: defaultAvailableMemoryMiB, want: sysinfo.AvailableMemoryMiB},
	}
	for _, tc := range cases {
		if reflect.ValueOf(tc.got).Pointer() != reflect.ValueOf(tc.want).Pointer() {
			t.Errorf("default %s is not the sysinfo read", tc.name)
		}
	}
}

// TestOverridesInstallAndRestore pins the seam contract: an override moves the
// read and the returned function restores the previous one. It seeds a known
// previous value so the restore is asserted without reading the host.
func TestOverridesInstallAndRestore(t *testing.T) {
	seedMemory := OverrideTotalMemoryGiBForTest(func() int { return 111 })
	seedCPUs := OverrideNumCPUsForTest(func() uint8 { return 3 })
	seedAvailable := OverrideAvailableMemoryMiBForTest(func() (uint32, bool) { return 7, true })
	t.Cleanup(func() {
		seedMemory()
		seedCPUs()
		seedAvailable()
	})

	restoreMemory := OverrideTotalMemoryGiBForTest(func() int { return 512 })
	restoreCPUs := OverrideNumCPUsForTest(func() uint8 { return 7 })
	restoreAvailable := OverrideAvailableMemoryMiBForTest(func() (uint32, bool) { return 42, true })

	if got := TotalMemoryGiB(); got != 512 {
		t.Errorf("TotalMemoryGiB = %d, want 512", got)
	}
	if got := NumCPUs(); got != 7 {
		t.Errorf("NumCPUs = %d, want 7", got)
	}
	if gotMiB, gotKnown := AvailableMemoryMiB(); gotMiB != 42 || !gotKnown {
		t.Errorf("AvailableMemoryMiB = (%d, %v), want (42, true)", gotMiB, gotKnown)
	}

	restoreMemory()
	restoreCPUs()
	restoreAvailable()

	if got := TotalMemoryGiB(); got != 111 {
		t.Errorf("TotalMemoryGiB after restore = %d, want 111", got)
	}
	if got := NumCPUs(); got != 3 {
		t.Errorf("NumCPUs after restore = %d, want 3", got)
	}
	if gotMiB, gotKnown := AvailableMemoryMiB(); gotMiB != 7 || !gotKnown {
		t.Errorf("AvailableMemoryMiB after restore = (%d, %v), want (7, true)", gotMiB, gotKnown)
	}
}

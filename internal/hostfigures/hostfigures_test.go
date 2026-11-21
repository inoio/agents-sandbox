package hostfigures

import (
	"reflect"
	"runtime"
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
// read: it compares the function's qualified name, so the assertion itself reads
// no host figure and does not rely on code-pointer uniqueness.
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
		if got, want := functionName(tc.got), functionName(tc.want); got != want {
			t.Errorf("default %s is %s, want %s", tc.name, got, want)
		}
	}
}

// functionName is the fully-qualified name of the function value fn, so the
// assertion distinguishes two function values that share a code pointer.
func functionName(fn any) string {
	return runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
}

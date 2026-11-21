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

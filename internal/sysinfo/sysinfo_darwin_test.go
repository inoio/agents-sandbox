//go:build darwin

package sysinfo

import "testing"

func TestTotalMemoryGiB(t *testing.T) {
	if got := TotalMemoryGiB(); got <= 0 {
		t.Fatalf("TotalMemoryGiB() = %d, want positive host memory", got)
	}
}

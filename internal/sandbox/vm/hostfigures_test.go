package vm

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
)

// TestHostFigureSeamDefaultsAreSysinfo pins the copied seam contract: each
// captured default is the production sysinfo read, so the pinned value in
// TestMain is the only divergence. This is the sole default-suite place the
// real host figures are read.
func TestHostFigureSeamDefaultsAreSysinfo(t *testing.T) {
	//testenv:allow asserts the captured seam defaults are the production host reads
	if got, want := capturedNumCPUs(), sysinfo.NumCPUs(); got != want {
		t.Errorf("capturedNumCPUs = %d, want sysinfo.NumCPUs %d", got, want)
	}
	//testenv:allow asserts the captured seam defaults are the production host reads
	if got, want := capturedTotalMemoryGiB(), sysinfo.TotalMemoryGiB(); got != want {
		t.Errorf("capturedTotalMemoryGiB = %d, want sysinfo.TotalMemoryGiB %d", got, want)
	}
}

// TestBootMaxMemoryMiBMapsTheSeam pins that the boot maximum derives from the
// seam's whole-GiB RAM through the MiB conversion, so pinning the seam moves it.
func TestBootMaxMemoryMiBMapsTheSeam(t *testing.T) {
	restore := totalMemoryGiB
	t.Cleanup(func() { totalMemoryGiB = restore })

	totalMemoryGiB = func() int { return 32 }
	if got, want := BootMaxMemoryMiB(), options.ParseMemoryGiB(32); got != want {
		t.Errorf("BootMaxMemoryMiB = %d, want %d", got, want)
	}
}

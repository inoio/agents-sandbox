package hostfigurestest

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/hostfigures"
)

// TestInstallSentinelsFromEnv pins the installer's contract: a no-op when the
// sentinel env var is unset, and the non-host sentinels when it is set. This is
// the test that stops the wiring in mocks.InitFailFastMocks from rotting.
func TestInstallSentinelsFromEnv(t *testing.T) {
	t.Run("unset is a no-op", func(t *testing.T) {
		t.Setenv(SentinelEnv, "")
		seed := hostfigures.OverrideTotalMemoryGiBForTest(func() int { return 20 })
		t.Cleanup(seed)

		InstallSentinelsFromEnv()

		if got := hostfigures.TotalMemoryGiB(); got != 20 { //testenv:allow the subtest seeded the seam value it asserts
			t.Errorf("TotalMemoryGiB = %d, want the seed 20 left untouched", got)
		}
	})

	t.Run("set pins the sentinels", func(t *testing.T) {
		t.Setenv(SentinelEnv, "1")
		restoreMemory := hostfigures.OverrideTotalMemoryGiBForTest(func() int { return 20 })
		restoreCPUs := hostfigures.OverrideNumCPUsForTest(func() uint8 { return 20 })
		restoreAvailable := hostfigures.OverrideAvailableMemoryMiBForTest(func() (uint32, bool) { return 20, true })
		t.Cleanup(func() {
			restoreMemory()
			restoreCPUs()
			restoreAvailable()
		})

		InstallSentinelsFromEnv()

		if got := hostfigures.TotalMemoryGiB(); got != 1 { //testenv:allow the installer test reads the seam it pinned
			t.Errorf("TotalMemoryGiB = %d, want the sentinel 1", got)
		}
		if got := hostfigures.NumCPUs(); got != 1 { //testenv:allow the installer test reads the seam it pinned
			t.Errorf("NumCPUs = %d, want the sentinel 1", got)
		}
		//testenv:allow the installer test reads the seam it pinned
		if gotMiB, gotKnown := hostfigures.AvailableMemoryMiB(); gotMiB != 1 || !gotKnown {
			t.Errorf("AvailableMemoryMiB = (%d, %v), want the sentinel (1, true)", gotMiB, gotKnown)
		}
	})
}

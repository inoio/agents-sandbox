//deadcode:ignore reason="test support: referenced only by _test.go files"
package hostfigurestest

import (
	"os"

	"github.com/inoio/agents-sandbox/internal/hostfigures"
)

// SentinelEnv names the environment variable the hermetic sentinel pass sets.
const SentinelEnv = "AGENTS_SANDBOX_HERMETIC_SENTINEL"

// InstallSentinelsFromEnv pins the host-figure seams to values deliberately
// different from any host when SentinelEnv is set, and is a no-op otherwise. A
// test that reads a real host figure rather than the seam then fails. Every
// TestMain that installs the fail-fast mocks installs it: mocks.InitFailFastMocks
// delegates here, and the hand-mirrored TestMains that install the families
// directly (the wrapper cannot reach their package) call it themselves. Two
// contract tests keep both routes wired.
func InstallSentinelsFromEnv() {
	if os.Getenv(SentinelEnv) == "" {
		return
	}
	hostfigures.OverrideTotalMemoryGiBForTest(func() int { return 1 })
	hostfigures.OverrideNumCPUsForTest(func() uint8 { return 1 })
	hostfigures.OverrideAvailableMemoryMiBForTest(func() (uint32, bool) { return 1, true })
}

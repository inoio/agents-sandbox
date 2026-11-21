// Package hostfigurestest installs the hermetic host-figure sentinels every test
// binary opts into through mocks.InitFailFastMocks. The sentinel pass sets
// SentinelEnv so a default-suite test that reads the real host figures instead
// of the seam fails. See specs/testing/test-isolation.md.
package hostfigurestest

import (
	"os"

	"github.com/inoio/agents-sandbox/internal/hostfigures"
)

// SentinelEnv names the environment variable the hermetic sentinel pass sets.
const SentinelEnv = "AGENTS_SANDBOX_HERMETIC_SENTINEL"

// InstallSentinelsFromEnv pins the host-figure seams to values deliberately
// different from any host when SentinelEnv is set, and is a no-op otherwise. A
// test that reads a real host figure rather than the seam then fails.
//
//deadcode:ignore reason="test support: called only by mocks.InitFailFastMocks and notify's TestMain"
func InstallSentinelsFromEnv() {
	if os.Getenv(SentinelEnv) == "" {
		return
	}
	hostfigures.OverrideTotalMemoryGiBForTest(func() int { return 1 })
	hostfigures.OverrideNumCPUsForTest(func() uint8 { return 1 })
	hostfigures.OverrideAvailableMemoryMiBForTest(func() (uint32, bool) { return 1, true })
}

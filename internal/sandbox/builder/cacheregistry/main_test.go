package cacheregistry

import "testing"

// TestMain dispatches the embedded registry daemon when this test binary was
// re-exec'd as it: Ensure spawns os.Executable(), which under `go test` is this
// binary, so the child must serve the daemon instead of re-running the suite.
func TestMain(m *testing.M) {
	RunIfDaemonInvocation()
	m.Run()
}

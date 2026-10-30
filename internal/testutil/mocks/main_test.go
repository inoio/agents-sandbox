package mocks

import "testing"

// TestMain installs the fail-fast defaults through the wrapper this package
// provides, the way every other test package does, so the wrapper is set up and
// covered by its own tests. A package cannot import itself, so the call is
// unqualified and the isolation contract test credits it as fullSetInstaller.
func TestMain(m *testing.M) {
	InitFailFastMocks(m)
}

//go:build integration

package image

import "os"

// backendDocker names the removed Docker-backed builder harness. The
// integration tests select it through AGENTS_SANDBOX_BUILDER.
const backendDocker = "docker"

// activeBackend reports the builder backend under test, defaulting to the
// production microsandbox (msb) backend.
func activeBackend() string {
	if backend := os.Getenv("AGENTS_SANDBOX_BUILDER"); backend != "" {
		return backend
	}
	return "msb"
}

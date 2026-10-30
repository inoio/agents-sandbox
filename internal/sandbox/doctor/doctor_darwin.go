//go:build darwin

package doctor

import (
	"errors"
	"runtime"

	"github.com/inoio/agents-sandbox/internal/docslink"
)

// checkPlatform validates that agents-sandbox is running on Apple Silicon.
func checkPlatform() error {
	if runtime.GOARCH != "arm64" {
		return appleSiliconRequiredError(runtime.GOARCH)
	}
	return nil
}

// appleSiliconRequiredError points an unsupported macOS build at the right
// release asset, since a wrong-architecture binary is fixed by re-downloading.
func appleSiliconRequiredError(arch string) error {
	return errors.New("only arm64 macOS (Apple Silicon) is supported; this binary is " + arch + "\n" +
		"  Download the Apple Silicon build:\n" +
		"    curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-darwin-arm64\n" +
		"  Docs: " + docslink.SectionURL("troubleshooting", "not-apple-silicon"))
}

// dockerRemedyHint is the macOS way to start the Docker daemon.
func dockerRemedyHint() string {
	return "  Start Docker Desktop or colima, then verify:\n" +
		"    open -a Docker   # or: colima start\n" +
		"    docker info\n" +
		"  Docs: " + docslink.SectionURL("troubleshooting", "docker-not-running")
}

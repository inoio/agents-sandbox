//go:build windows

package doctor

import (
	"errors"
	"runtime"

	"github.com/inoio/agents-sandbox/internal/docslink"
)

// checkPlatform validates that agents-sandbox runs on a Windows host that can
// back the microsandbox VM: Windows 11, or Windows Server with nested
// virtualization, with the Windows Hypervisor Platform enabled. The spike
// checks the architecture only.
func checkPlatform() error {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return nil
	default:
		return errors.New("only amd64 and arm64 Windows are supported; this binary is " + runtime.GOARCH +
			"\n  Docs: " + docslink.SectionURL("troubleshooting", "kvm-unavailable"))
	}
}

// dockerRemedyHint is the Windows way to start a container engine.
func dockerRemedyHint() string {
	return "  Start Docker Desktop, then verify it serves the Docker API:\n" +
		"    docker info\n" +
		"  Docs: " + docslink.SectionURL("troubleshooting", "container-engine-not-running")
}

package image

import (
	"bytes"
	"fmt"
	"os"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
)

// markerOpenCodeInstall is the placeholder comment in the embedded runner
// Dockerfile where the agent install block is inserted at render time.
const markerOpenCodeInstall = "# AGENT_INSTALL_BLOCK"

// DockerfileFromImageSpec renders the runner Dockerfile with the agent's
// install block parameterized from its ImageSpec.
func DockerfileFromImageSpec(spec agent.ImageSpec) []byte {
	block := fmt.Sprintf(`
ARG %s

ENV %s=true
LABEL %s="$%s"

RUN %s
`, spec.VersionArg, spec.DisableUpdateEnv, spec.VersionLabel, spec.VersionArg, spec.InstallCommand)
	return bytes.Replace(embeddedDockerfile, []byte(markerOpenCodeInstall), []byte(block), 1)
}

// ResolveRunnerDockerfile returns the project Dockerfile when one exists,
// otherwise the embedded runner Dockerfile rendered for the given agent.
func ResolveRunnerDockerfile(a agent.Agent) []byte {
	cp := configpaths.Get()
	if data, err := os.ReadFile(cp.ProjectDockerfile()); err == nil {
		return data
	}
	return DockerfileFromImageSpec(a.ImageSpec())
}

// ResolveDockerfile returns the project Dockerfile bytes, falling back to the
// embedded runner Dockerfile. It is owned by the image module; session must
// not redeclare it.
func ResolveDockerfile() []byte {
	cp := configpaths.Get()
	if data, err := os.ReadFile(cp.ProjectDockerfile()); err == nil {
		return data
	}
	return embeddedDockerfile
}

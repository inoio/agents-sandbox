package image

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

// runnerTag is the Docker tag of the runner image: it keeps the
// agents-sandbox/runner- namespace so the image is distinguishable in the
// system Docker daemon.
func runnerTag(projectSlug, agentName string) string {
	return naming.DockerImageRef(projectSlug, agentName)
}

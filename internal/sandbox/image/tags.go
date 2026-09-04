package image

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

func runnerTag(projectSlug, agentName string) string {
	return naming.ImagePrefix + projectSlug + ":" + agentName + "-latest"
}

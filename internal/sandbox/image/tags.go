package image

import (
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
)

func runnerTag(projectSlug, agentName string) string {
	return naming.ImagePrefix + projectSlug + ":" + agentName + "-latest"
}

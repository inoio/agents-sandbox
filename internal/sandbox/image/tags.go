package image

import (
	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

func runnerTag(projectSlug, agentName string) string {
	return naming.ImagePrefix + projectSlug + ":" + agentName + "-latest"
}

// baseTag returns the shared runner base image reference for the agent and
// dind switch. The moving -latest tag is rebuilt when its content identity
// changes; old content becomes dangling and is reclaimed by docker pruning.
func baseTag(a agent.Agent, dind bool) string {
	tag := naming.ImagePrefix + "base:" + a.Name() + "-latest"
	if dind {
		tag += "-dind"
	}
	return tag
}

package image

import (
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
)

// TagDigest returns the shortened digest used as an msb image tag suffix for
// the given full Docker image digest. Both the runner image tag and image
// pruning derive this short form from the full digest, so the mapping lives in
// one place.
func TagDigest(fullDigest string) string {
	return git.HashID(fullDigest)
}

func runnerTag(projectSlug, agentName string) string {
	return naming.ImagePrefix + projectSlug + ":" + agentName + "-latest"
}

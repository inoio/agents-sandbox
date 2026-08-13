package image

import (
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/git"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
)

func imageTag(projectSlug, imageDigest string) string {
	return naming.ImagePrefix + projectSlug + ":" + git.HashID(imageDigest)
}

func runnerTag(projectSlug string) string {
	return naming.ImagePrefix + projectSlug + ":latest"
}

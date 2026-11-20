package agent

import (
	"context"
	"fmt"
	"strings"
)

var gooseGitHubLatestURL = "https://api.github.com/repos/aaif-goose/goose/releases/latest"

// latestGooseVersion returns the newest goose release (leading "v" stripped)
// from the GitHub releases/latest endpoint. The image installs the release
// asset, so the release tag is authoritative.
func latestGooseVersion(ctx context.Context) (string, error) {
	tag, err := latestGitHubReleaseTag(ctx, gooseGitHubLatestURL, gooseName)
	if err != nil {
		return "", err
	}
	version, found := strings.CutPrefix(tag, "v")
	if !found || version == "" {
		return "", fmt.Errorf("latest %s release: unexpected tag %q", gooseName, tag)
	}
	return version, nil
}

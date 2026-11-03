package agent

import (
	"context"
	"fmt"
	"strings"
)

// ohMyPiGitHubLatestURL is a var (not const) so the tests can point it at an
// httptest server.
var ohMyPiGitHubLatestURL = "https://api.github.com/repos/can1357/oh-my-pi/releases/latest"

// latestOhMyPiVersion returns the newest Oh My Pi release (leading "v"
// stripped) from the GitHub releases/latest endpoint. The image installs the
// release asset, so the release tag, not the npm package, is authoritative.
func latestOhMyPiVersion(ctx context.Context) (string, error) {
	tag, err := latestGitHubReleaseTag(ctx, ohMyPiGitHubLatestURL, ohMyPiName)
	if err != nil {
		return "", err
	}
	version, found := strings.CutPrefix(tag, "v")
	if !found || version == "" {
		return "", fmt.Errorf("latest %s release: unexpected tag %q", ohMyPiName, tag)
	}
	return version, nil
}

package agent

import (
	"context"
	"fmt"
	"strings"
)

// codexGitHubLatestURL is a var (not const) so the tests can point it at an
// httptest server.
var codexGitHubLatestURL = "https://api.github.com/repos/openai/codex/releases/latest"

// codexReleaseTagPrefix prefixes every Codex CLI release tag, e.g. "rust-v0.164.0".
const codexReleaseTagPrefix = "rust-v"

// latestCodexVersion returns the newest stable Codex CLI version by querying
// the GitHub releases/latest endpoint and stripping the tag prefix.
func latestCodexVersion(ctx context.Context) (string, error) {
	tag, err := latestGitHubReleaseTag(ctx, codexGitHubLatestURL, codexName)
	if err != nil {
		return "", err
	}
	version, found := strings.CutPrefix(tag, codexReleaseTagPrefix)
	if !found || version == "" {
		return "", fmt.Errorf("latest %s release: unexpected tag %q", codexName, tag)
	}
	return version, nil
}

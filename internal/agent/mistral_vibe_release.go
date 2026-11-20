package agent

import (
	"context"
	"fmt"
)

// mistralVibePyPIURL is PyPI's JSON document for the mistral-vibe project. The
// image installs that package, so its current release is authoritative; the
// GitHub releases carry only the separate vibe-acp server binaries.
var mistralVibePyPIURL = "https://pypi.org/pypi/mistral-vibe/json"

// latestMistralVibeVersion returns the newest mistral-vibe release on PyPI.
func latestMistralVibeVersion(ctx context.Context) (string, error) {
	var project struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := fetchReleaseJSON(ctx, mistralVibePyPIURL, mistralVibeName, &project); err != nil {
		return "", err
	}
	if project.Info.Version == "" {
		return "", fmt.Errorf("latest %s release: empty version", mistralVibeName)
	}
	return project.Info.Version, nil
}

package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// gooseGitHubReleasesURL lists goose's recent GitHub releases, newest first.
var gooseGitHubReleasesURL = "https://api.github.com/repos/aaif-goose/goose/releases?per_page=20"

// gooseCLIAssets are the release assets the image installs, one per supported
// architecture; a release counts only once both are uploaded.
var gooseCLIAssets = []string{
	"goose-x86_64-unknown-linux-musl.tar.gz",
	"goose-aarch64-unknown-linux-musl.tar.gz",
}

type gooseReleaseAsset struct {
	Name string `json:"name"`
}

type gooseRelease struct {
	TagName    string              `json:"tag_name"`
	Draft      bool                `json:"draft"`
	Prerelease bool                `json:"prerelease"`
	Assets     []gooseReleaseAsset `json:"assets"`
}

// hasCLIAssets reports whether the release carries every Linux CLI archive the
// image downloads.
func (r gooseRelease) hasCLIAssets() bool {
	for _, asset := range gooseCLIAssets {
		if !slices.ContainsFunc(r.Assets, func(a gooseReleaseAsset) bool { return a.Name == asset }) {
			return false
		}
	}
	return true
}

// latestGooseVersion returns the newest stable goose release (leading "v"
// stripped) whose Linux CLI archives are published. GitHub's releases/latest
// can name a release before its assets are uploaded, and an unpinned build of
// such a version fails on a 404, so the newest complete release wins instead.
func latestGooseVersion(ctx context.Context) (string, error) {
	var releases []gooseRelease
	if err := fetchReleaseJSON(ctx, gooseGitHubReleasesURL, gooseName, &releases); err != nil {
		return "", err
	}
	for _, release := range releases {
		if release.Draft || release.Prerelease || !release.hasCLIAssets() {
			continue
		}
		version, found := strings.CutPrefix(release.TagName, "v")
		if !found || version == "" {
			return "", fmt.Errorf("latest %s release: unexpected tag %q", gooseName, release.TagName)
		}
		return version, nil
	}
	return "", fmt.Errorf(
		"latest %s release: no goose release lists %s",
		gooseName,
		strings.Join(gooseCLIAssets, " and "),
	)
}

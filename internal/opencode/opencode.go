// Package opencode resolves and compares opencode release versions the same
// way opencode's own autoupdate does: the latest release is read from the
// GitHub releases/latest endpoint, and version strings compare numerical
// dot-separated segments.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// gitHubLatestURL is a var (not const) so the tests can point it at an httptest
// server via overrideLatestURL.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable endpoint URL
var gitHubLatestURL = "https://api.github.com/repos/anomalyco/opencode/releases/latest"

type githubRelease struct {
	TagName string `json:"tag_name"`
}

//nolint:gochecknoglobals // test seam
var LatestVersion = latestVersion

// LatestVersion returns the newest stable opencode release string (leading "v"
// stripped) by querying the GitHub releases/latest endpoint.
func latestVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gitHubLatestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build latest-version request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "opencode-sandbox")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("latest opencode release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest opencode release: unexpected status %d", resp.StatusCode)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decode latest opencode release: %w", err)
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

// VersionCompare compares two semantic version strings numerically segment by
// segment, ignoring a leading "v" and treating missing trailing segments as 0.
// It returns -1, 0, or 1. Pre-release/build suffixes are ignored for ordering.
func VersionCompare(a, b string) int {
	sa, sb := strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	pa, pb := splitNumeric(sa), splitNumeric(sb)
	n := max(len(pa), len(pb))
	for i := range n {
		va, vb := 0, 0
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

// splitNumeric splits a dotted version string into leading numeric segments,
// stopping at a non-numeric segment (e.g. "-beta.1").
func splitNumeric(s string) []int {
	var out []int
	for part := range strings.SplitSeq(s, ".") {
		digits := 0
		for digits < len(part) && part[digits] >= '0' && part[digits] <= '9' {
			digits++
		}
		if digits == 0 {
			break
		}
		n, _ := strconv.Atoi(part[:digits])
		out = append(out, n)
	}
	return out
}

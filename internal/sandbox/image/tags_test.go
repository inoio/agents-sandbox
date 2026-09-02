package image

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/git"
)

func TestTagDigestShortensFullDigest(t *testing.T) {
	full := "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12"
	got := TagDigest(full)
	if len(got) != 14 {
		t.Errorf("TagDigest length = %d, want 14", len(got))
	}
	if got != git.HashID(full) {
		t.Errorf("TagDigest(%q) = %q, want %q", full, got, git.HashID(full))
	}
}

func TestRunnerTagIsPerAgent(t *testing.T) {
	if got := runnerTag("myproject", "opencode"); got != "opencode-sandbox/runner-myproject:opencode-latest" {
		t.Errorf("runnerTag(opencode) = %q", got)
	}
	if got := runnerTag("myproject", "pi"); got != "opencode-sandbox/runner-myproject:pi-latest" {
		t.Errorf("runnerTag(pi) = %q", got)
	}
}

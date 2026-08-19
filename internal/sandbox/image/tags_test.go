package image

import (
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
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

func TestImageTagUsesTagDigest(t *testing.T) {
	full := "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12"
	tag := imageTag("myproject", full)
	want := naming.ImagePrefix + "myproject:" + TagDigest(full)
	if tag != want {
		t.Errorf("imageTag = %q, want %q", tag, want)
	}
	if !strings.HasSuffix(tag, TagDigest(full)) {
		t.Errorf("imageTag %q must end in the shortened digest %q", tag, TagDigest(full))
	}
}

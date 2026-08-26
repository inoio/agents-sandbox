package image

import (
	"os"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestResolveDockerfile_ProjectFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	content := "FROM custom/base:latest\n"
	testutil.WriteFile(t, configpaths.Get().ProjectConfigDir(), "Dockerfile", content)

	if got := string(ResolveDockerfile()); got != content {
		t.Errorf("ResolveDockerfile() = %q, want %q", got, content)
	}
}

func TestResolveDockerfile_FallsBackToEmbedded(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	got := string(ResolveDockerfile())
	if got == "" {
		t.Error("ResolveDockerfile() = empty, want embedded fallback content")
	}
	if _, err := os.Stat(configpaths.Get().ProjectDockerfile()); !os.IsNotExist(err) {
		t.Fatalf("expected no project Dockerfile, but one exists at %s", configpaths.Get().ProjectDockerfile())
	}
	if got != string(embeddedDockerfile) {
		t.Errorf("ResolveDockerfile() = %q, want embedded %q", got, string(embeddedDockerfile))
	}
}

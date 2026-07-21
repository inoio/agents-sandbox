package opencodemsb

import (
	"context"
	"testing"
)

func TestReferencesBaseDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner:base\nRUN echo hi\n")
	if !ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=true for Dockerfile with base FROM")
	}
}

func TestReferencesBaseReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for non-base Dockerfile")
	}
}

func TestReferencesBaseIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-msb/runner:base\nFROM debian:trixie-slim\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for commented FROM")
	}
}

func TestImageTag(t *testing.T) {
	got := ImageTag("sha256:abc123def456")
	expected := "opencode-msb/runner:sha256-abc123def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestEnsureImageReturnsErrorWithoutDocker(t *testing.T) {
	_, _, err := EnsureImage(context.Background(), EmbeddedDockerfile, true)
	if err == nil {
		t.Error("expected error when Docker is not available")
	}
}

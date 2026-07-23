package sandbox

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("p-abc123", "sha256-def456")
	expected := "p-abc123-opencode-home-sha256-def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestHomeVolumeNameSanitizesColon(t *testing.T) {
	got := HomeVolumeName("p-abc123", "sha256:def456")
	expected := "p-abc123-opencode-home-sha256-def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestNewVolumeManager(t *testing.T) {
	l := log.New(nil, false)
	vm := NewVolumeManager(l)
	if vm.logger == nil {
		t.Error("expected logger to be set")
	}
}

package sandbox

import (
	"path/filepath"
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

func TestFallbackHomePath(t *testing.T) {
	l := log.New(nil, false)
	vm := NewVolumeManager(true, "/tmp/state", l)
	got := vm.fallbackHomePath("p-abc", "sha256-def")
	expected := filepath.Join("/tmp/state", "state", "p-abc", "home", "sha256-def")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestNewVolumeManager(t *testing.T) {
	l := log.New(nil, false)
	vm := NewVolumeManager(true, "/tmp/state", l)
	if !vm.fallback {
		t.Error("expected fallback=true")
	}
	if vm.stateDir != "/tmp/state" {
		t.Errorf("expected stateDir=/tmp/state, got %q", vm.stateDir)
	}
}

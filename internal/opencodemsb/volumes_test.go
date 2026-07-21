package opencodemsb

import (
	"path/filepath"
	"testing"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("p-abc123", "sha256-def456")
	expected := "p-abc123-opencode-home-sha256-def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestFallbackHomePath(t *testing.T) {
	vm := NewVolumeManager(true, "/tmp/state")
	got := vm.fallbackHomePath("p-abc", "sha256-def")
	expected := filepath.Join("/tmp/state", "state", "p-abc", "home", "sha256-def")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestNewVolumeManager(t *testing.T) {
	vm := NewVolumeManager(true, "/tmp/state")
	if !vm.fallback {
		t.Error("expected fallback=true")
	}
	if vm.stateDir != "/tmp/state" {
		t.Errorf("expected stateDir=/tmp/state, got %q", vm.stateDir)
	}
}

package sandbox

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D", "sha256:abc123def456")
	expected := "opencode-msb-home-myproj-aBc1234D-3k5q07ywpibwp5"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestHomeVolumeNameDifferentInputs(t *testing.T) {
	// HashID hashes the full input string, so different inputs produce different hashes
	got := HomeVolumeName("myproj-aBc1234D", "sha256:abc123def456")
	got2 := HomeVolumeName("myproj-aBc1234D", "abc123def456")
	if got == got2 {
		t.Errorf("expected different hashes for different inputs, got %q and %q", got, got2)
	}
}

func TestNewVolumeManager(t *testing.T) {
	l := output.NewPrinter(nil, false)
	vm := NewVolumeManager(l)
	if vm.logger == nil {
		t.Error("expected logger to be set")
	}
}

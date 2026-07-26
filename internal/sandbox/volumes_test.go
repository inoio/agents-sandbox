package sandbox

import (
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
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
	l := output.NewPrinter(nil, false)
	vm := NewVolumeManager(l)
	if vm.logger == nil {
		t.Error("expected logger to be set")
	}
}

func TestExtractNamedVolumes(t *testing.T) {
	configJSON := `{
		"name": "test-sandbox",
		"volumes": {
			"/home/dev": {"named": "my-home-vol"},
			"/workspace": {"bind": "/host/path"}
		}
	}`
	got := extractNamedVolumes(configJSON)
	if len(got) != 1 {
		t.Fatalf("expected 1 named volume, got %d", len(got))
	}
	if got[0] != "my-home-vol" {
		t.Errorf("expected 'my-home-vol', got %q", got[0])
	}
}

func TestExtractNamedVolumesEmpty(t *testing.T) {
	configJSON := `{"name": "test"}`
	got := extractNamedVolumes(configJSON)
	if len(got) != 0 {
		t.Fatalf("expected 0 named volumes, got %d", len(got))
	}
}

func TestExtractNamedVolumesInvalidJSON(t *testing.T) {
	got := extractNamedVolumes("not json")
	if len(got) != 0 {
		t.Fatalf("expected 0 named volumes for invalid JSON, got %d", len(got))
	}
}

func TestCloneVolumeName(t *testing.T) {
	name := cloneVolumeName("my-source-vol")
	if !strings.HasPrefix(name, "my-source-vol-clone-") {
		t.Errorf("expected clone name to start with 'my-source-vol-clone-', got %q", name)
	}
}

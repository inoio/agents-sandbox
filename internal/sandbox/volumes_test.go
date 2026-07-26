package sandbox

import (
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D", "sha256:abc123def456")
	expected := "opencode-msb-home-myproj-aBc1234D-xRX898Gl"
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
	source := "opencode-msb-home-myproj-aBc1234D-xRX898Gl"
	got := cloneVolumeName(source)
	if !strings.HasPrefix(got, "opencode-msb-clone-myproj-aBc1234D-xRX898Gl-") {
		t.Errorf("expected clone name to start with 'opencode-msb-clone-myproj-aBc1234D-xRX898Gl-', got %q", got)
	}
}

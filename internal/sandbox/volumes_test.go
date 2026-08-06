package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D", "")
	expectedPrefix := "opencode-msb-home-myproj-aBc1234D-"
	if !strings.HasPrefix(got, expectedPrefix) {
		t.Errorf("expected prefix %q, got %q", expectedPrefix, got)
	}
	suffix := strings.TrimPrefix(got, expectedPrefix)
	if len(suffix) != 15 {
		t.Errorf("expected 15-char timestamp, got %d chars: %q", len(suffix), suffix)
	}
}

func TestHomeVolumeNameDifferentInputs(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D", "sha256:abc123def456")
	if !strings.HasPrefix(got, "opencode-msb-home-myproj-aBc1234D-") {
		t.Errorf("unexpected name format: %q", got)
	}
}

func TestHomeVolumeNameTimestamp(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := HomeVolumeName("myproject", "")
	after := time.Now().UTC().Add(time.Second)

	if !strings.HasPrefix(got, "opencode-msb-home-myproject-") {
		t.Fatalf("expected prefix, got %q", got)
	}
	suffix := strings.TrimPrefix(got, "opencode-msb-home-myproject-")
	if len(suffix) != 15 {
		t.Fatalf("expected 15-char timestamp, got %d chars: %q", len(suffix), suffix)
	}
	ts, err := time.Parse("20060102T150405", suffix)
	if err != nil {
		t.Fatalf("timestamp %q does not parse: %v", suffix, err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v not within expected range", ts)
	}
}

func TestHomeVolumeNameDigestIgnored(t *testing.T) {
	got1 := HomeVolumeName("proj", "sha256:abc123")
	got2 := HomeVolumeName("proj", "")
	got3 := HomeVolumeName("proj", "different")
	if !strings.HasPrefix(got1, "opencode-msb-home-proj-") {
		t.Errorf("got1 prefix wrong: %q", got1)
	}
	if !strings.HasPrefix(got2, "opencode-msb-home-proj-") {
		t.Errorf("got2 prefix wrong: %q", got2)
	}
	if !strings.HasPrefix(got3, "opencode-msb-home-proj-") {
		t.Errorf("got3 prefix wrong: %q", got3)
	}
}

func TestNewVolumeManager(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	vm := NewVolumeManager(&testUI)
	if vm.ui == nil {
		t.Error("expected ui to be set")
	}
}

func TestPrefillVolumeRunsCopyCommand(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI
	client := &MockMsbClient{}
	vm := NewVolumeManager(ui)

	err := vm.prefillVolume(
		context.Background(),
		client,
		"myproject",
		"test-home-vol",
		"opencode-msb/runner-test:latest",
		ui,
	)
	if err != nil {
		t.Fatalf("prefillVolume failed: %v", err)
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 created prefill sandbox, got %d", len(client.CreatedSandboxes))
	}
	if len(client.RemovedSandboxes) != 1 {
		t.Fatalf("expected 1 removed prefill sandbox, got %d", len(client.RemovedSandboxes))
	}
}

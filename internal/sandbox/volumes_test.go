package sandbox

import (
	"context"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
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
	testUI := testutil.NewTestio(t)
	vm := NewVolumeManager(&testUI)
	if vm.ui == nil {
		t.Error("expected ui to be set")
	}
}

func TestPrefillVolumeRunsCopyCommand(t *testing.T) {
	testUI := testutil.NewTestio(t)
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

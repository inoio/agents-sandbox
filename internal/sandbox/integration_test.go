//go:build integration

package sandbox

import (
	"testing"
)

func TestSameHomeVolumeInUseNoSandboxes(t *testing.T) {
	got, inUse, err := sameHomeVolumeInUse(t.Context(), "my-vol", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inUse {
		t.Error("expected not in use when no sandboxes exist")
	}
	if got != "" {
		t.Errorf("expected empty sandbox name, got %q", got)
	}
}

func TestSameBranchSessionExistsNoSandbox(t *testing.T) {
	exists, err := sameBranchSessionExists(t.Context(), "nonexistent-sandbox")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected false for nonexistent sandbox")
	}
}

package sandbox

import (
	"testing"
)

func TestFilterSandboxesByPrefix(t *testing.T) {
	handles := []sandboxHandle{
		{name: "opencode-msb-proj-main"},
		{name: "opencode-msb-other-feat"},
		{name: "someone-elses-sandbox"},
		{name: "random"},
	}
	got := filterSandboxes(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d", len(got))
	}
	if got[0] != "opencode-msb-proj-main" {
		t.Errorf("expected first match, got %q", got[0])
	}
	if got[1] != "opencode-msb-other-feat" {
		t.Errorf("expected second match, got %q", got[1])
	}
}

func TestFilterVolumesByPrefix(t *testing.T) {
	handles := []volumeHandle{
		{name: "proj-opencode-home-sha256-abc"},
		{name: "other-opencode-home-sha256-def"},
		{name: "random-volume"},
	}
	got := filterVolumes(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(got))
	}
}

func TestFilterImagesByPrefix(t *testing.T) {
	handles := []imageHandle{
		{reference: "opencode-msb/runner:sha256-abc"},
		{reference: "opencode-msb/runner:base"},
		{reference: "python:3.12"},
	}
	got := filterImages(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 images, got %d", len(got))
	}
}

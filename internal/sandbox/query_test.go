package sandbox

import (
	"testing"
)

func TestFilterSandboxesByPrefix(t *testing.T) {
	handles := []sandboxHandle{
		{name: "opencode-msb-sb-proj-main"},
		{name: "opencode-msb-sb-other-feat"},
		{name: "opencode-msb-task-prefill-proj-1719432000"},
		{name: "opencode-msb-task-clone-proj-1719432000"},
		{name: "someone-elses-sandbox"},
		{name: "random"},
	}
	got := filterSandboxes(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 session sandboxes, got %d", len(got))
	}
	if got[0] != "opencode-msb-sb-proj-main" {
		t.Errorf("expected first match, got %q", got[0])
	}
	if got[1] != "opencode-msb-sb-other-feat" {
		t.Errorf("expected second match, got %q", got[1])
	}
}

func TestFilterVolumesByPrefix(t *testing.T) {
	handles := []volumeHandle{
		{name: "opencode-msb-home-proj-aBc1234D"},
		{name: "opencode-msb-clone-proj-aBc1234D-1719432000"},
		{name: "old-style-proj-opencode-home-sha256-abc"},
		{name: "random-volume"},
	}
	got := filterVolumes(handles)
	if len(got) != 1 {
		t.Fatalf("expected 1 home volume, got %d", len(got))
	}
	if got[0] != "opencode-msb-home-proj-aBc1234D" {
		t.Errorf("expected home volume, got %q", got[0])
	}
}

func TestFilterImagesByPrefix(t *testing.T) {
	handles := []imageHandle{
		{reference: "opencode-msb/runner-proj-aBc1234D:xRX898Gl"},
		{reference: "opencode-msb/runner-otherproj-eFg5678I:abc12345"},
		{reference: "python:3.12"},
	}
	got := filterImages(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 project images, got %d", len(got))
	}
	if got[0] != "opencode-msb/runner-proj-aBc1234D:xRX898Gl" {
		t.Errorf("expected first match, got %q", got[0])
	}
}

package sandbox

import (
	"testing"
)

func TestFilterSandboxesByPrefix(t *testing.T) {
	handles := []sandboxHandle{
		{name: "opencode-msb-vm-proj-aBc1234D"},
		{name: "opencode-msb-vm-other-feat"},
		{name: "opencode-msb-task-prefill-proj-1719432000"},
		{name: "someone-elses-sandbox"},
		{name: "random"},
	}
	got := filterSandboxes(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 project VMs, got %d", len(got))
	}
	if got[0] != "opencode-msb-vm-proj-aBc1234D" {
		t.Errorf("expected first match, got %q", got[0])
	}
	if got[1] != "opencode-msb-vm-other-feat" {
		t.Errorf("expected second match, got %q", got[1])
	}
}

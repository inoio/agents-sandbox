package naming

import "testing"

func TestLabelConstants(t *testing.T) {
	if LabelProject != "org.opencode-sandbox.project" {
		t.Errorf("LabelProject = %q, want org.opencode-sandbox.project", LabelProject)
	}
	if LabelImage != "org.opencode-sandbox.image" {
		t.Errorf("LabelImage = %q, want org.opencode-sandbox.image", LabelImage)
	}
}

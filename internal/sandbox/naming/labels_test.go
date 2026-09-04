package naming

import "testing"

func TestLabelConstants(t *testing.T) {
	if LabelProject != "org.agents-sandbox.project" {
		t.Errorf("LabelProject = %q, want org.agents-sandbox.project", LabelProject)
	}
	if LabelImage != "org.agents-sandbox.image" {
		t.Errorf("LabelImage = %q, want org.agents-sandbox.image", LabelImage)
	}
}

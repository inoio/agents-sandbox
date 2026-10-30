package options

import "testing"

func TestModeSkipsVM(t *testing.T) {
	if ModeRun.SkipsVM() {
		t.Error("ModeRun must not skip the VM")
	}
	if !ModeSkipVM.SkipsVM() || !ModePreview.SkipsVM() {
		t.Error("ModeSkipVM and ModePreview must skip the VM")
	}
}

func TestModePreviews(t *testing.T) {
	if ModeRun.Previews() || ModeSkipVM.Previews() {
		t.Error("only ModePreview previews")
	}
	if !ModePreview.Previews() {
		t.Error("ModePreview must preview")
	}
}

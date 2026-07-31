package sandbox

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestProjectVMName(t *testing.T) {
	got := projectVMName("myproj-aBc1234D")
	want := "opencode-msb-vm-myproj-aBc1234D"
	if got != want {
		t.Errorf("projectVMName(%q) = %q, want %q", "myproj-aBc1234D", got, want)
	}
}

func TestProjectVMNameTruncation(t *testing.T) {
	longSlug := "p-abcdef-very-long-slug-that-exceeds-the-128-byte-limit-and-then-some-more-padding"
	got := projectVMName(longSlug)
	if len(got) > maxSandboxNameLen {
		t.Errorf("expected name <= %d bytes, got %d", maxSandboxNameLen, len(got))
	}
	if len(got) < len(projectVMPrefix) {
		t.Errorf("name too short: %q", got)
	}
}

func TestProjectVMPrefixConstant(t *testing.T) {
	if projectVMPrefix != "opencode-msb-vm-" {
		t.Errorf("expected prefix %q, got %q", "opencode-msb-vm-", projectVMPrefix)
	}
}

func TestBuildProjectVMEnvIncludesWorkspaces(t *testing.T) {
	envMap := map[string]string{
		"FOO": "bar",
	}
	buildProjectVMEnv(envMap, nil)
	if envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Errorf("expected OPENCODE_EXPERIMENTAL_WORKSPACES=true, got %q", envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"])
	}
	if envMap["PATH"] == "" {
		t.Error("expected PATH to have a fallback value")
	}
}

func TestBuildProjectVMEnvMergesImageEnvs(t *testing.T) {
	imageEnvs := map[string]string{
		"MY_KEY": "my_value",
		"PATH":   "/custom/path",
	}
	envMap := map[string]string{}
	buildProjectVMEnv(envMap, imageEnvs)
	if envMap["MY_KEY"] != "my_value" {
		t.Errorf("expected MY_KEY=my_value from image envs, got %q", envMap["MY_KEY"])
	}
	// Image env PATH should override the fallback PATH that buildProjectVMEnv sets.
	if envMap["PATH"] != "/custom/path" {
		t.Errorf("expected PATH=/custom/path from image envs, got %q", envMap["PATH"])
	}
	if envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Error("expected OPENCODE_EXPERIMENTAL_WORKSPACES to be set")
	}
}

func TestEnsureProjectVMCreatesWhenNotFound(t *testing.T) {
	notFoundErr := &msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"}
	decision, err := decideVMAction(notFoundErr, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionCreate {
		t.Errorf("expected vmActionCreate, got %v", decision)
	}
}

func TestEnsureProjectVMConnectsWhenRunning(t *testing.T) {
	decision, err := decideVMAction(nil, msb.SandboxStatusRunning)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionConnect {
		t.Errorf("expected vmActionConnect, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenStopped(t *testing.T) {
	decision, err := decideVMAction(nil, msb.SandboxStatusStopped)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenCrashed(t *testing.T) {
	decision, err := decideVMAction(nil, msb.SandboxStatusCrashed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart for crashed, got %v", decision)
	}
}

package vm

import (
	"testing"
)

func TestBuildProjectVMEnvMergesImageEnvs(t *testing.T) {
	imageEnvs := map[string]string{
		"MY_KEY":                           "my_value",
		"PATH":                             "/custom/path",
		"OPENCODE_EXPERIMENTAL_WORKSPACES": "true",
	}
	env := buildProjectVMEnv(map[string]string{}, imageEnvs)
	if env["MY_KEY"] != "my_value" {
		t.Errorf("expected MY_KEY=my_value from image envs, got %q", env["MY_KEY"])
	}
	// Image env PATH should override any host env default.
	if env["PATH"] != "/custom/path" {
		t.Errorf("expected PATH=/custom/path from image envs, got %q", env["PATH"])
	}
	if env["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Error("expected OPENCODE_EXPERIMENTAL_WORKSPACES to be passed through from image envs")
	}
}

func TestBuildProjectVMEnvKeepsExistingEntries(t *testing.T) {
	hostEnv := map[string]string{"FOO": "bar"}
	env := buildProjectVMEnv(hostEnv, map[string]string{"BAZ": "qux"})
	if env["FOO"] != "bar" {
		t.Errorf("expected FOO=bar preserved, got %q", env["FOO"])
	}
	if env["BAZ"] != "qux" {
		t.Errorf("expected BAZ=qux from image envs, got %q", env["BAZ"])
	}
	if len(hostEnv) != 1 {
		t.Errorf("expected the host env to be left unmodified, got %v", hostEnv)
	}
}

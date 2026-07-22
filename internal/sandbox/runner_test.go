package sandbox

import (
	"testing"
)

func TestParseMemoryGigabytes(t *testing.T) {
	got := parseMemory("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryMegabytes(t *testing.T) {
	got := parseMemory("512M")
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryPlainNumber(t *testing.T) {
	got := parseMemory("2048")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryLowercase(t *testing.T) {
	got := parseMemory("2g")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestSandboxNameTruncation(t *testing.T) {
	got := sandboxName("p-abcdef", "feat-very-long-branch-name-that-exceeds-the-limit-and-more")
	if len(got) > 128 {
		t.Errorf("expected name <= 128 bytes, got %d", len(got))
	}
}

func TestBuildEnvMap(t *testing.T) {
	envExtra := []string{"FOO=bar", "BAZ=qux"}
	got := buildEnvMap(envExtra)
	if got["HOME"] != "/home/dev" {
		t.Errorf("expected HOME=/home/dev, got %q", got["HOME"])
	}
	if got["SANDBOX_USER"] != "dev" {
		t.Errorf("expected SANDBOX_USER=dev, got %q", got["SANDBOX_USER"])
	}
	if got["SHELL"] != "/bin/bash" {
		t.Errorf("expected SHELL=/bin/bash, got %q", got["SHELL"])
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", got["FOO"])
	}
}

func TestReadSandboxEnvMissing(t *testing.T) {
	env := readSandboxEnv()
	if len(env) != 0 {
		t.Errorf("expected 0 env vars when .sandbox/env missing, got %d", len(env))
	}
}

package configpaths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

// TestFailFastConfigPathsPanics verifies that every method of the default
// fail-fast implementation panics, so any test that reaches real path
// resolution without opting in fails loudly rather than silently resolving.
func TestFailFastConfigPathsPanics(t *testing.T) {
	f := &failFastConfigPaths{}
	a, _ := agent.Lookup("opencode")

	cases := []struct {
		name string
		fn   func()
	}{
		{"UserConfigDir", func() { f.UserConfigDir() }},
		{"UserCacheDir", func() { f.UserCacheDir() }},
		{"UserStateDir", func() { f.UserStateDir() }},
		{"UserAgentConfigDir", func() { f.UserAgentConfigDir(a) }},
		{"UserEnvFile", func() { f.UserEnvFile() }},
		{"UserEnvSecretFile", func() { f.UserEnvSecretFile() }},
		{"UserEnvSecretYAMLFile", func() { f.UserEnvSecretYAMLFile() }},
		{"ProjectConfigDir", func() { f.ProjectConfigDir() }},
		{"ProjectAgentConfigDir", func() { f.ProjectAgentConfigDir(a) }},
		{"ProjectEnvFile", func() { f.ProjectEnvFile() }},
		{"ProjectEnvSecretFile", func() { f.ProjectEnvSecretFile() }},
		{"ProjectEnvSecretYAMLFile", func() { f.ProjectEnvSecretYAMLFile() }},
		{"ProjectDockerfile", func() { f.ProjectDockerfile() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPanics(t, tc.fn)
		})
	}
}

// TestEnsureMockConfigDirectoryFailurePath triggers the error branch of
// ensureMockConfigDirectory by asking it to create a directory whose parent
// path component is a regular file, which MkdirAll cannot satisfy.
func TestEnsureMockConfigDirectoryFailurePath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(blocker, "sub")
	if got := ensureMockConfigDirectory(path); got != path {
		t.Errorf("ensureMockConfigDirectory(%q) = %q, want %q", path, got, path)
	}
}

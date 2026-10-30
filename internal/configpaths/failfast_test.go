package configpaths

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
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
		{"UserSlugConfigDir", func() { f.UserSlugConfigDir("widget-bbb") }},
		{"UserCacheDir", func() { f.UserCacheDir() }},
		{"UserStateDir", func() { f.UserStateDir() }},
		{"UserAgentConfigDir", func() { f.UserAgentConfigDir(a) }},
		{"UserAgentConfigDirCandidates", func() { f.UserAgentConfigDirCandidates(a) }},
		{"UserHomeDir", func() { _, _ = f.UserHomeDir() }},
		{"UserEnvFile", func() { f.UserEnvFile() }},
		{"UserEnvSecretFile", func() { f.UserEnvSecretFile() }},
		{"UserEnvSecretYAMLFile", func() { f.UserEnvSecretYAMLFile() }},
		{"ProjectConfigDir", func() { f.ProjectConfigDir() }},
		{"ProjectConfigDirAbs", func() { _ = f.ProjectConfigDirAbs() }},
		{"ProjectAgentConfigDir", func() { f.ProjectAgentConfigDir(a) }},
		{"ProjectAgentConfigDirCandidates", func() { f.ProjectAgentConfigDirCandidates(a) }},
		{"ProjectEnvFile", func() { f.ProjectEnvFile() }},
		{"ProjectEnvSecretFile", func() { f.ProjectEnvSecretFile() }},
		{"ProjectEnvSecretYAMLFile", func() { f.ProjectEnvSecretYAMLFile() }},
		{"ProjectDockerfile", func() { f.ProjectDockerfile() }},
		{"IgnoredWorktreeConfigDir", func() { f.IgnoredWorktreeConfigDir() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPanics(t, tc.fn)
		})
	}
}

// TestEnsureMockConfigDirectoryFailurePath triggers the error branch of
// ensureMockConfigDirectory by asking it to create a directory whose parent
// path component is a regular file, which MkdirAll cannot satisfy. The helper
// still returns the requested path, and the error must be reported on stdout
// so a mock tree that could not be created is visible rather than silently
// absent.
func TestEnsureMockConfigDirectoryFailurePath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "sub")

	readEnd, writeEnd, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	stdout := os.Stdout
	os.Stdout = writeEnd
	t.Cleanup(func() {
		// Restore and close even if the helper panics or a check fatals, so the
		// redirected global can never leak into a later test.
		os.Stdout = stdout
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})

	got := ensureMockConfigDirectory(path)
	os.Stdout = stdout
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatal(err)
	}

	if got != path {
		t.Errorf("ensureMockConfigDirectory(%q) = %q, want %q", path, got, path)
	}
	if !strings.Contains(string(output), blocker) {
		t.Errorf("failure output = %q, want it to report the MkdirAll error for %q", output, blocker)
	}
}

// TestEnsureMockConfigDirectoryMode pins that the helper requests 0o750 (owner
// rwx, group rx, no world access) so a mock tree never becomes wider than the
// real resolver's directories. MkdirAll masks the mode with the process umask,
// so assert no bits outside 0o750 and no world access rather than the exact
// value: a stricter umask (e.g. 0077) is still correct.
func TestEnsureMockConfigDirectoryMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "created", "nested")
	if got := ensureMockConfigDirectory(path); got != path {
		t.Fatalf("ensureMockConfigDirectory(%q) = %q, want %q", path, got, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got&^0o750 != 0 || got&0o007 != 0 {
		t.Errorf("created directory mode = %o, want no wider than 0o750", got)
	}
}

// TestFailFastConfigPathsFactory pins the default factory's concrete value:
// the panic a test hits on reaching real path resolution must come from the
// fail-fast implementation, not from a nil ConfigPaths dereference.
func TestFailFastConfigPathsFactory(t *testing.T) {
	f, ok := FailFastConfigPaths().(*failFastConfigPaths)
	if !ok {
		t.Fatalf("FailFastConfigPaths() = %T, want *failFastConfigPaths", FailFastConfigPaths())
	}
	assertPanics(t, func() { f.UserConfigDir() })
}

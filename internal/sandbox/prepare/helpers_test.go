package prepare

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// opencodeAgent returns the default opencode profile for tests that need a real
// agent but don't otherwise care which one it is.
func opencodeAgent(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

// opencodeResolved returns the default opencode profile with its capabilities
// resolved, for tests that build a sessionIdentity or drive a resolved-value
// consumer directly.
func opencodeResolved(t *testing.T) agent.Resolved {
	t.Helper()
	return agent.Resolve(opencodeAgent(t))
}

// opencodeProvider returns the opencode profile's DaemonProvider so tests can
// build the exact commands the production path shells out with.
func opencodeProvider(t *testing.T) agent.DaemonProvider {
	t.Helper()
	p, ok := agent.AsDaemonProvider(opencodeAgent(t))
	if !ok {
		t.Fatal("opencode agent does not implement DaemonProvider")
	}
	return p
}

// testVMKey returns a stable project/agent key for unit tests that exercise
// the VM lifecycle without needing a real git project slug.
func testVMKey() state.Key {
	return state.Key{Slug: "testproj", Agent: "opencode"}
}

// seedHomeState writes hs as the key's HomeState through the recovery
// transaction, overwriting any existing document. Tests only.
func seedHomeState(k state.Key, hs state.HomeState) error {
	return state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = hs
		return true, nil
	})
}

// seedRootState writes rs as the launcher-global RootState through the root
// transaction. Tests only.
func seedRootState(rs state.RootState) error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		*tx.State() = rs
		return tx.Commit()
	})
	return err
}

// mustIdentity resolves a fixture cwd to its project identity, failing the test
// on the now-fallible ResolveIdentity: fixtures always point at real paths.
func mustIdentity(t testing.TB, cwd string) git.Identity {
	t.Helper()
	identity, err := git.ResolveIdentity(cwd)
	require.NoError(t, err)
	return identity
}

// unresolvableIdentityDir returns a directory whose .git file points at a git
// directory that does not exist, so git.ResolveIdentity fails for it while the
// directory itself still resolves.
func unresolvableIdentityDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".git"),
		[]byte("gitdir: "+filepath.Join(t.TempDir(), "gone.git")+"\n"),
		0o644,
	))
	return dir
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func joinStrings(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p)
		b.WriteByte('|')
	}
	return b.String()
}

func applySandboxOpts(cfg *msbSdk.SandboxConfig, opts []msbSdk.SandboxOption) {
	for _, o := range opts {
		o(cfg)
	}
}

// statePresent reports whether the key has a readable state file, failing the
// test on any read error other than a missing file.
func statePresent(t *testing.T, k state.Key) bool {
	t.Helper()
	_, err := state.ReadState(k)
	switch {
	case err == nil:
		return true
	case errors.Is(err, state.ErrStateNotFound):
		return false
	default:
		t.Fatalf("ReadState(%v): %v", k, err)
		return false
	}
}

// fakeAgent implements only the core Agent interface with no capabilities, so
// the daemon/worktree paths must no-op for it.
type fakeAgent struct{}

// failingStateDirConfigPaths overrides only the state directory, delegating
// every other path to the (nil) embedded interface. It is used to force
// read/write errors on the per-project state file by pointing UserStateDir at a
// regular file, so the parent "directory" cannot be created or read.
type failingStateDirConfigPaths struct {
	configpaths.ConfigPaths

	stateDir string
}

func (f failingStateDirConfigPaths) UserStateDir() string { return f.stateDir }

// withFailingStateDir installs a ConfigPaths whose UserStateDir is a regular
// file, so any attempt to read or write <stateDir>/... fails.
func withFailingStateDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "userstate")
	if err := os.WriteFile(stateFile, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths {
		return failingStateDirConfigPaths{stateDir: stateFile}
	}
	t.Cleanup(func() { configpaths.Get = orig })
}

// assertDetachedHelperLaunch fails the test unless the recorded detached calls
// are exactly one launch of binary under the helper marker; the marker shape is
// what the kill path matches on, so the callers (dockerd, the daemon) are pinned
// to it.
func assertDetachedHelperLaunch(t *testing.T, calls []msb.MockDetachedCall, marker, binary string) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("detached calls = %+v, want one", calls)
	}
	if calls[0].Cmd != "env" {
		t.Errorf("launch binary = %q, want %q", calls[0].Cmd, "env")
	}
	args := calls[0].Args
	if len(args) < 2 || args[0] != "AGENTS_SANDBOX_DETACHED_JOB="+marker || args[1] != binary {
		t.Errorf("launch args = %v, want the %s marker then %q", args, marker, binary)
	}
}

// dockerdBinaryCheckCmd is the probe vm.SetUpSandbox runs to detect dockerd in
// the VM. A mock answering it with a failure keeps a PrepareSandbox test from
// starting dockerd; it must match the command in package vm.
const dockerdBinaryCheckCmd = "command -v dockerd >/dev/null 2>&1"

func (fakeAgent) Name() string { return "fake" }

func (fakeAgent) Experimental() bool { return false }

func (fakeAgent) ConfigDirName() string { return "fake" }

func (fakeAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{}
}

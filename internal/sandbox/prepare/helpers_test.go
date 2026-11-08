package prepare

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// upgradeCheckerAgent implements both the core Agent interface and the
// UpgradeChecker capability so the shared release resolver reaches its
// LatestVersion call without touching the network.
type upgradeCheckerAgent struct{}

func (upgradeCheckerAgent) Name() string { return "upgradechecker" }

func (upgradeCheckerAgent) Experimental() bool { return false }

func (upgradeCheckerAgent) ConfigDirName() string { return "upgradechecker" }

func (upgradeCheckerAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{}
}

func (upgradeCheckerAgent) LatestVersion(context.Context) (string, error) { return "9.9.9", nil }

func (upgradeCheckerAgent) NewerThan(_, _ string) (bool, error) { return true, nil }

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

// resolvedRunOptions returns opts with ResolvedAgent set as runoptions.Build
// would: the profile named by opts.Agent (the default when empty), with its
// capabilities resolved. Tests that construct RunOptions directly use it so the
// prepare stages see the run boundary's resolved agent.
func resolvedRunOptions(t *testing.T, opts options.RunOptions) options.RunOptions {
	t.Helper()
	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return opts
	}
	opts.ResolvedAgent = agent.Resolve(a)
	return opts
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

// exhaustedPortMappings claims every host port in the serve-only probe range,
// so a serve-only resolution finds no free port to publish the agent on.
func exhaustedPortMappings() []ports.PortMapping {
	mappings := make([]ports.PortMapping, 0, ports.ProbeRange)
	for port := ports.ServeOnlyBasePort; port < ports.ServeOnlyBasePort+ports.ProbeRange; port++ {
		mappings = append(mappings, ports.PortMapping{HostPort: port, GuestPort: port})
	}
	return mappings
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

func (fakeAgent) Name() string { return "fake" }

func (fakeAgent) Experimental() bool { return false }

func (fakeAgent) ConfigDirName() string { return "fake" }

func (fakeAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{}
}

// readAndResolveReconfigDecision reads the live project VM through client and
// resolves the reconfiguration decision from it, as planReconfigStage does,
// with empty host env and secrets.
func readAndResolveReconfigDecision(
	ctx context.Context,
	client msb.Client,
	volumes *volume.Manager,
	k state.Key,
	opts options.RunOptions,
	imageInfo image.ImageInfo, homeVolume string, homeState state.HomeState,
	configFiles *reprovision.ConfigFiles,
	ui termio.UI,
) (reconfigDecision, error) {
	live, err := readLiveProjectVM(ctx, client, k, configFiles.Keys, ui)
	if err != nil {
		return reconfigDecision{}, err
	}
	return resolveReconfigDecision(
		ctx,
		volumes,
		k,
		opts,
		imageInfo,
		homeVolume,
		homeState,
		configFiles,
		envsecret.EnvSecrets{},
		live,
		ui,
	)
}

// reconfigFixture wires the mocks resolveReconfigDecision reads its live VM from.
type reconfigFixture struct {
	client  *msb.MockMsbClient
	volumes *volume.Manager
	ui      *termiotest.Mock
	cfs     *reprovision.ConfigFiles
	homeVol string
}

func (f reconfigFixture) decide(t *testing.T, opts options.RunOptions, hs state.HomeState) (bool, error) {
	t.Helper()
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		f.client,
		f.volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		opts,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: hs.ImageDigest},
		f.homeVol,
		hs,
		f.cfs,
		f.ui,
	)
	return decision.Recreate, err
}

// seedAgentState writes the agent version/source into this project's HomeState,
// preserving any other fields already present.
func seedAgentState(t *testing.T, k state.Key, version, source string) {
	t.Helper()
	hs, err := state.ReadState(k)
	if err != nil {
		hs = &state.HomeState{}
	}
	hs.AgentVersion = version
	hs.AgentSource = source
	if err := seedHomeState(k, *hs); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
}

// withMockAgentLatestVersion pins the shared agent-latest-version lookup, which
// the build resolver and the upgrade check both consult.
func withMockAgentLatestVersion(t *testing.T, v string) {
	t.Helper()
	orig := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return v, nil }
	t.Cleanup(func() { agentLatestVersion = orig })
}

package vm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	sandboximage "github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// cloneOrigin is the shared origin URL for the identity fixtures, so two clones
// map to one repo slug while keeping distinct checkout slugs.
const cloneOrigin = "https://example.com/acme/widget.git"

// newOriginClone builds a minimal origin-holding repo layout (a .git dir whose
// config names the shared origin), the same fixture shape used by internal/git's
// identity tests. It returns the symlink-resolved path so assertions match what
// ResolveIdentity derives.
func newOriginClone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	config := "[remote \"origin\"]\n\turl = " + cloneOrigin + "\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	return root
}

// installPrepareSandboxDockerMock wires the docker and agent-version mocks a
// PrepareSandbox run needs, and returns a pointer to the runner image tag the
// build inspected (the tag the pipeline resolved).
func installPrepareSandboxDockerMock(t *testing.T) *string {
	t.Helper()
	sandboximage.WithMockAgentVersion(t, "1.0.0")
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	inspectedTag := new(string)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			*inspectedTag = ref
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})
	return inspectedTag
}

// installPrepareSandboxMSBMock installs an msb mock whose project VM does not
// exist yet (so PrepareSandbox takes the create path) and whose created sandbox
// is usable by the daemon step.
func installPrepareSandboxMSBMock(t *testing.T) *msb.MockMsbClient {
	t.Helper()
	createdSb := &msb.MockSandbox{
		Name_:    "created-vm",
		FSValue_: msb.NewTestFS(nil, nil),
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)
	return mock
}

// installPrepareSandboxDaemonMock makes the agent daemon look healthy inside the
// VM so PrepareSandbox does not wait for it.
func installPrepareSandboxDaemonMock(t *testing.T) {
	t.Helper()
	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })
}

// seedMatchingHomeState seeds a home state whose image/agent version matches the
// mocked image, so home resolution reuses the volume without prompting.
func seedMatchingHomeState(t *testing.T, k state.Key) {
	t.Helper()
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.0.0",
		AgentSource:  agentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
}

// createdProjectVMConfig applies the recorded CreateSandbox options for the
// named project VM and returns the config it was created with. Other
// CreateSandbox calls (a home-volume prefill) are ignored.
func createdProjectVMConfig(t *testing.T, mock *msb.MockMsbClient, name string) msbSdk.SandboxConfig {
	t.Helper()
	for _, call := range mock.CreatedSandboxCalls {
		if call.Name != name {
			continue
		}
		var cfg msbSdk.SandboxConfig
		for _, opt := range call.Opts {
			opt(&cfg)
		}
		return cfg
	}
	t.Fatalf("no CreateSandbox call for %q; calls: %v", name, mock.CreatedSandboxes)
	return msbSdk.SandboxConfig{}
}

// createdProjectVMNames returns the names of the project-VM-shaped CreateSandbox
// calls, filtering out the home-volume prefill task sandbox.
func createdProjectVMNames(mock *msb.MockMsbClient) []string {
	var names []string
	for _, name := range mock.CreatedSandboxes {
		info := naming.ParseVMName(name)
		if info.Slug != "" && !info.Task {
			names = append(names, name)
		}
	}
	return names
}

// TestPrepareSandboxUsesCheckoutSlugForStateAndRepoSlugForLabel pins the #144
// split for two clones of one origin: each clone gets its own VM name (and
// runner image tag) from its checkout slug while the grouping label stays the
// shared repo slug, so `list --label project=<repo>` groups both.
func TestPrepareSandboxUsesCheckoutSlugForStateAndRepoSlugForLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	firstClone := newOriginClone(t)
	secondClone := newOriginClone(t)
	firstIdentity := mustIdentity(t, firstClone)
	secondIdentity := mustIdentity(t, secondClone)
	if firstIdentity.RepoSlug != secondIdentity.RepoSlug {
		t.Fatalf("repo slugs differ for one origin: %q vs %q", firstIdentity.RepoSlug, secondIdentity.RepoSlug)
	}
	if firstIdentity.CheckoutSlug == secondIdentity.CheckoutSlug {
		t.Fatalf("checkout slugs collide across clones: %q", firstIdentity.CheckoutSlug)
	}

	inspectedTag := installPrepareSandboxDockerMock(t)
	installPrepareSandboxDaemonMock(t)
	ui := termio.NewTestMock(t)

	t.Chdir(firstClone)
	firstKey := state.Key{Slug: firstIdentity.CheckoutSlug, Agent: "opencode"}
	seedMatchingHomeState(t, firstKey)
	firstMock := installPrepareSandboxMSBMock(t)
	firstSession, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox in first clone: %v", err)
	}
	defer firstSession.Cleanup()
	firstTag := *inspectedTag
	firstName := projectVMName(firstKey)
	firstLabels := createdProjectVMConfig(t, firstMock, firstName).Labels

	t.Chdir(secondClone)
	secondKey := state.Key{Slug: secondIdentity.CheckoutSlug, Agent: "opencode"}
	seedMatchingHomeState(t, secondKey)
	secondMock := installPrepareSandboxMSBMock(t)
	secondSession, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox in second clone: %v", err)
	}
	defer secondSession.Cleanup()
	secondTag := *inspectedTag
	secondName := projectVMName(secondKey)
	secondLabels := createdProjectVMConfig(t, secondMock, secondName).Labels

	if firstName == secondName {
		t.Fatalf("both clones created the same VM name %q", firstName)
	}
	if got := createdProjectVMNames(firstMock); len(got) != 1 || got[0] != firstName {
		t.Errorf("first clone created project VMs %v, want exactly [%s]", got, firstName)
	}
	if got := createdProjectVMNames(secondMock); len(got) != 1 || got[0] != secondName {
		t.Errorf("second clone created project VMs %v, want exactly [%s]", got, secondName)
	}
	if firstTag != naming.DockerImageRef(firstIdentity.CheckoutSlug, "opencode") {
		t.Errorf("first clone image tag = %q, want the checkout-slug tag", firstTag)
	}
	if secondTag != naming.DockerImageRef(secondIdentity.CheckoutSlug, "opencode") {
		t.Errorf("second clone image tag = %q, want the checkout-slug tag", secondTag)
	}
	for clone, labels := range map[string]map[string]string{"first": firstLabels, "second": secondLabels} {
		if got := labels[naming.LabelProject]; got != firstIdentity.RepoSlug {
			t.Errorf("%s clone %s label = %q, want the repo slug %q",
				clone, naming.LabelProject, got, firstIdentity.RepoSlug)
		}
	}
	if got := firstSession.Key(); got != firstKey {
		t.Errorf("first session key = %+v, want %+v", got, firstKey)
	}
	if got := secondSession.Key(); got != secondKey {
		t.Errorf("second session key = %+v, want %+v", got, secondKey)
	}
}

// TestPrepareSandboxOutsideWorktreeUsesItsOwnKey pins the design change: a
// worktree outside its checkout (this repo's own
// ~/.local/share/opencode/worktree/<n>/<name> layout) is not refused; it runs as
// its own checkout by hashing its worktree root, so its state key, VM name and
// image tag carry the worktree-root-derived slug rather than the checkout's.
func TestPrepareSandboxOutsideWorktreeUsesItsOwnKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	repo := testutil.InitRepo(t)
	testutil.RunGit(t, repo, "remote", "add", "origin", cloneOrigin)
	outside := filepath.Join(t.TempDir(), "wt")
	testutil.RunGit(t, repo, "worktree", "add", outside)
	recordSharedGitDirOutsideManagedMounts(t, repo, outside)

	identity := mustIdentity(t, outside)
	checkoutIdentity := mustIdentity(t, repo)
	if !identity.OutsideWorktree {
		t.Fatalf("expected %q to resolve as an outside worktree", outside)
	}
	if identity.CheckoutSlug == checkoutIdentity.CheckoutSlug {
		t.Fatalf("outside worktree shares its checkout's slug %q", identity.CheckoutSlug)
	}
	if identity.RepoSlug != checkoutIdentity.RepoSlug {
		t.Errorf("outside worktree repo slug = %q, want the shared %q",
			identity.RepoSlug, checkoutIdentity.RepoSlug)
	}

	inspectedTag := installPrepareSandboxDockerMock(t)
	installPrepareSandboxDaemonMock(t)
	ui := termio.NewTestMock(t)

	t.Chdir(outside)
	key := state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"}
	seedMatchingHomeState(t, key)
	mock := installPrepareSandboxMSBMock(t)

	session, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox in an outside worktree: %v", err)
	}
	defer session.Cleanup()

	if got := session.Key(); got != key {
		t.Errorf("session key = %+v, want the worktree-root-derived %+v", got, key)
	}
	if got := createdProjectVMNames(mock); len(got) != 1 || got[0] != projectVMName(key) {
		t.Errorf("created sandboxes = %v, want the worktree-root-derived %q",
			got, projectVMName(key))
	}
	if *inspectedTag != naming.DockerImageRef(identity.CheckoutSlug, "opencode") {
		t.Errorf("image tag = %q, want the worktree-root-derived tag", *inspectedTag)
	}
	// The slug is only half the identity: /workspace must bind the worktree
	// root — not the checkout it shares a common dir with, and not the cwd.
	worktreeRoot, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", outside, err)
	}
	mountConfig := createdProjectVMConfig(t, mock, projectVMName(key))
	if got := mountConfig.Volumes[defaultTargetDir].Bind; got != worktreeRoot {
		t.Errorf("%s mount source = %q, want the worktree root %q", defaultTargetDir, got, worktreeRoot)
	}
	assertSharedGitDirMountWired(t, identity, mountConfig.Volumes)
}

// recordSharedGitDirOutsideManagedMounts rewrites the worktree's .git file to
// reach the shared git dir through a symlink under /var/tmp. t.TempDir lies
// under the managed /tmp guest mount on Linux, where the shared git dir cannot
// be mounted, so without this the test could not observe the mount (#426).
func recordSharedGitDirOutsideManagedMounts(t *testing.T, repo, worktree string) {
	t.Helper()
	//nolint:usetesting // t.TempDir lies under the managed /tmp guest mount on Linux
	linkDir, err := os.MkdirTemp("/var/tmp", "agents-sandbox-test-")
	if err != nil {
		t.Skipf("no writable /var/tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(linkDir) })
	link := filepath.Join(linkDir, "repo")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatalf("symlink %q: %v", link, err)
	}
	gitdir := filepath.Join(link, ".git", "worktrees", filepath.Base(worktree))
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite %s/.git: %v", worktree, err)
	}
}

// assertSharedGitDirMountWired checks that PrepareSandbox binds an outside
// worktree's shared git dir at the path its .git file records (#426).
func assertSharedGitDirMountWired(t *testing.T, identity git.Identity, volumes map[string]msbSdk.MountConfig) {
	t.Helper()
	if got := volumes[identity.RecordedSharedGitDir].Bind; got != identity.SharedGitDir {
		t.Errorf("%s mount source = %q, want the shared git dir %q",
			identity.RecordedSharedGitDir, got, identity.SharedGitDir)
	}
}

// TestPrepareSandboxSkipVMKeepsLegacySlugWhileMigrationPending pins the
// read-only prepare: with the VM skipped, a pending migration prints the "would"
// notice and the rest of the pipeline keeps the legacy repo slug, so no
// post-migration artifact is presented as if the migration had run.
func TestPrepareSandboxSkipVMKeepsLegacySlugWhileMigrationPending(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must produce distinct repo/checkout slugs, both %q", identity.RepoSlug)
	}

	legacyKey := state.Key{Slug: identity.RepoSlug, Agent: "opencode"}
	seedMatchingHomeState(t, legacyKey)

	inspectedTag := installPrepareSandboxDockerMock(t)
	installPrepareSandboxDaemonMock(t)
	ui := termio.NewTestMock(t)
	t.Chdir(clone)
	installPrepareSandboxMSBMock(t)

	session, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, true, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox (skip VM): %v", err)
	}
	defer session.Cleanup()

	notice := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{
		"Would migrate this checkout to its own project identity",
		state.SlugDir(identity.RepoSlug),
		state.SlugDir(identity.CheckoutSlug),
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("dry-run notice missing %q:\n%s", want, notice)
		}
	}
	if *inspectedTag != naming.DockerImageRef(identity.RepoSlug, "opencode") {
		t.Errorf("image tag = %q, want the legacy repo-slug tag %q",
			*inspectedTag, naming.DockerImageRef(identity.RepoSlug, "opencode"))
	}
	if session.Key().Slug != identity.RepoSlug {
		t.Errorf("session slug = %q, want the legacy repo slug %q", session.Key().Slug, identity.RepoSlug)
	}
	if statePresent(t, state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"}) {
		t.Error("dry run moved state onto the checkout slug")
	}
}

// TestPrepareSandboxMigratesOnFirstRun covers the real migration: with legacy
// state and a legacy VM present, the first run destroys the legacy VM, moves the
// state onto the checkout slug, and recreates the VM under the checkout name.
func TestPrepareSandboxMigratesOnFirstRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)

	legacyKey := state.Key{Slug: identity.RepoSlug, Agent: "opencode"}
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"}
	seedMatchingHomeState(t, legacyKey)

	legacyHandle := &msb.MockSandboxHandle{
		Name_:     projectVMName(legacyKey),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: msb.NewMockSandbox(msb.SandboxOpts{}),
	}
	inspectedTag := installPrepareSandboxDockerMock(t)
	installPrepareSandboxDaemonMock(t)
	ui := termio.NewTestMock(t)

	mock := installPrepareSandboxMSBMock(t)
	mock.GetSandboxFn = func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == projectVMName(legacyKey) {
			return legacyHandle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}

	t.Chdir(clone)
	session, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox (migrate): %v", err)
	}
	defer session.Cleanup()

	if !legacyHandle.Destroyed {
		t.Error("legacy origin-keyed VM was not destroyed")
	}
	if !statePresent(t, checkoutKey) {
		t.Error("state file did not arrive under the checkout slug")
	}
	if got := createdProjectVMNames(mock); len(got) != 1 || got[0] != projectVMName(checkoutKey) {
		t.Errorf("created sandboxes = %v, want the checkout-slug name %q",
			got, projectVMName(checkoutKey))
	}
	if *inspectedTag != naming.DockerImageRef(identity.CheckoutSlug, "opencode") {
		t.Errorf("image tag = %q, want the checkout-slug tag", *inspectedTag)
	}
	if session.Key() != checkoutKey {
		t.Errorf("session key = %+v, want %+v", session.Key(), checkoutKey)
	}
}

// TestPrepareSandboxAdoptionFailureReturnsError pins the fail-closed wiring on
// the run path: an unreadable legacy state aborts PrepareSandbox before it
// resolves a version, ensures an image, or creates a VM.
func TestPrepareSandboxAdoptionFailureReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)
	installPrepareSandboxMSBMock(t)

	legacyDir := state.KeyDir(state.Key{Slug: identity.RepoSlug, Agent: "opencode"})
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy state dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, "state.yaml", `": [`)

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui); err == nil ||
		!strings.Contains(err.Error(), "check for a pending legacy identity") {
		t.Fatalf(
			"PrepareSandbox with an unreadable legacy state: error = %v, want the fail-closed predicate error",
			err,
		)
	}
}

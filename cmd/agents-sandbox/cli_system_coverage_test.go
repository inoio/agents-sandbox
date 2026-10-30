package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

// originCloneURL is the shared origin URL for the identity fixtures, so two
// clones map to one repo slug while keeping distinct checkout slugs.
const originCloneURL = "https://example.com/acme/widget.git"

// newOriginClone builds a minimal origin-holding repo layout (a .git dir whose
// config names the shared origin), the same fixture shape internal/git's
// identity tests use. It returns the symlink-resolved path so assertions match
// what ResolveIdentity derives.
func newOriginClone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	config := "[remote \"origin\"]\n\turl = " + originCloneURL + "\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	return root
}

// TestBuildUsesCheckoutSlug pins that the standalone build tags the runner image
// with the checkout slug, matching run/volume ops, so two clones of one origin
// build distinct images.
func TestBuildUsesCheckoutSlug(t *testing.T) {
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

	t.Chdir(secondClone)
	cmd, _ := setupCommandFixtures(t, cmdBuild)
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	// The default noop Docker mock reports no image ID, which would fall back to
	// the name ref. Return a real ID so the build exercises the content ref.
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     "sha256:9f2c1a7b4d6e0f3199999999999999999999999999999999999999999999999999",
				Config: &dockerspec.DockerOCIImageConfig{},
			}, nil
		},
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("build: %v", err)
	}
	want := naming.ContentImageRef(secondIdentity.CheckoutSlug, defaultAgentName, "9f2c1a7b4d6e0f31")
	if !slices.Contains(mock.LoadedImages, want) {
		t.Errorf("build loaded images %v, want the second clone's checkout content ref %q", mock.LoadedImages, want)
	}
}

// TestBuildDryRunPrintsMigrationNoticeAndMovesNothing pins the all-or-nothing dry
// run for build: adoption prints the "Would migrate" notice but leaves the
// migration pending — the legacy state survives and no checkout state appears.
//
// It deliberately does not assert the effective slug: build --dry-run returns
// before image.Build, and the notice always prints both the legacy and the
// target tag, so neither seam could falsify a slug here. The effective-slug pin
// is TestVolumeOpsKeepLegacySlugWhileMigrationPending, which observes the tag
// the op ensured through a docker inspect mock.
func TestBuildDryRunPrintsMigrationNoticeAndMovesNothing(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)

	cmd, ui := setupCommandFixtures(t, cmdBuild, "--dry-run")
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	repoKey := state.Key{Slug: identity.RepoSlug, Agent: defaultAgentName}
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: defaultAgentName}
	if err := seedHomeState(repoKey, state.HomeState{
		HomeVolume: fmt.Sprintf("agents-sandbox-home-%s-20260101T000000", identity.RepoSlug),
	}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("build --dry-run: %v", err)
	}

	if _, err := state.ReadState(repoKey); err != nil {
		t.Errorf("legacy state must survive a dry run: %v", err)
	}
	if _, err := state.ReadState(checkoutKey); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("dry run must not create the checkout-keyed state; got %v", err)
	}

	notice := strings.Join(ui.WarnCalls, "\n")
	if !strings.Contains(notice, "Would migrate this checkout to its own project identity") {
		t.Errorf("expected the dry-run migration notice, got:\n%s", notice)
	}
}

// TestBuildChecksDockerBeforeAdoption pins that build resolves its Docker
// prerequisite before adopting. On a host with broken Docker the command must
// fail without destroying the legacy VM or moving the state, matching run/shell
// and the volume ops, which all check their prerequisites before adopting.
func TestBuildChecksDockerBeforeAdoption(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)

	cmd, _ := setupCommandFixtures(t, cmdBuild)
	doctor.MockedCheckDocker(t, false)

	// A running legacy VM that adoption would destroy if it ran first.
	legacyVM := &msb.MockSandboxHandle{
		Name_:   naming.LegacyVMPrefix + identity.RepoSlug + "-" + defaultAgentName,
		Status_: msbSdk.SandboxStatusRunning,
	}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return legacyVM, nil
		},
	}
	msb.WithMsbMock(t, mock)

	repoKey := state.Key{Slug: identity.RepoSlug, Agent: defaultAgentName}
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: defaultAgentName}
	if err := seedHomeState(repoKey, state.HomeState{
		HomeVolume: fmt.Sprintf("agents-sandbox-home-%s-20260101T000000", identity.RepoSlug),
	}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "docker not available") {
		t.Fatalf("build with broken docker: error = %v, want a 'docker not available' error", err)
	}
	if legacyVM.DidDestroy() {
		t.Error("build must not destroy the legacy VM before its Docker prerequisite passes")
	}
	if _, err := state.ReadState(repoKey); err != nil {
		t.Errorf("the legacy state must stay in place when the Docker check fails first: %v", err)
	}
	if _, err := state.ReadState(checkoutKey); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("the state must not move before the Docker check passes; checkout state = %v", err)
	}
}

// TestVolumeOpsUseCheckoutSlug pins that a volume op resolves the checkout slug
// of the clone it runs in, not the repo slug shared with every clone.
func TestVolumeOpsUseCheckoutSlug(t *testing.T) {
	firstClone := newOriginClone(t)
	secondClone := newOriginClone(t)
	firstIdentity := mustIdentity(t, firstClone)
	secondIdentity := mustIdentity(t, secondClone)
	if firstIdentity.CheckoutSlug == secondIdentity.CheckoutSlug {
		t.Fatalf("checkout slugs collide across clones: %q", firstIdentity.CheckoutSlug)
	}

	t.Chdir(secondClone)
	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdReset, "--dry-run")
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	key := state.Key{Slug: secondIdentity.CheckoutSlug, Agent: defaultAgentName}
	if err := seedHomeState(key, state.HomeState{
		HomeVolume: fmt.Sprintf("agents-sandbox-home-%s-20260101T000000", secondIdentity.CheckoutSlug),
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("volume reset --dry-run: %v", err)
	}

	joined := strings.Join(ui.InfoCalls, "\n")
	if !strings.Contains(joined, secondIdentity.CheckoutSlug) {
		t.Errorf("volume reset must resolve the second clone's checkout slug %q, got:\n%s",
			secondIdentity.CheckoutSlug, joined)
	}
	if strings.Contains(joined, secondIdentity.RepoSlug) {
		t.Errorf("volume reset must not resolve the shared repo slug %q, got:\n%s",
			secondIdentity.RepoSlug, joined)
	}
}

// TestVolumeOpsKeepLegacySlugWhileMigrationPending pins adoption in the volume
// ops: a pending legacy identity is noticed but left unapplied by a dry run, and
// the op resolves against the legacy repo slug.
func TestVolumeOpsKeepLegacySlugWhileMigrationPending(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)

	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdReset, "--dry-run")
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	// A dry run must not ensure (inspect or build) the runner image: the
	// would-do message needs only the state-derived old volume.
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			t.Errorf("volume reset --dry-run must not inspect the runner image, got ref %q", ref)
			return client.ImageInspectResult{Config: &dockerspec.DockerOCIImageConfig{}}, nil
		},
	})

	repoKey := state.Key{Slug: identity.RepoSlug, Agent: defaultAgentName}
	if err := seedHomeState(repoKey, state.HomeState{
		HomeVolume: fmt.Sprintf("agents-sandbox-home-%s-20260101T000000", identity.RepoSlug),
	}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("volume reset --dry-run: %v", err)
	}

	joined := strings.Join(ui.InfoCalls, "\n")
	if !strings.Contains(joined, identity.RepoSlug) {
		t.Errorf("a pending legacy identity must keep the op on the repo slug %q, got:\n%s",
			identity.RepoSlug, joined)
	}
	if strings.Contains(joined, identity.CheckoutSlug) {
		t.Errorf("a dry run must not resolve the checkout slug %q while the migration is pending, got:\n%s",
			identity.CheckoutSlug, joined)
	}

	notice := strings.Join(ui.WarnCalls, "\n")
	if !strings.Contains(notice, "Would migrate this checkout to its own project identity") {
		t.Errorf("expected the dry-run migration notice, got:\n%s", notice)
	}
	if _, err := state.ReadState(
		state.Key{Slug: identity.CheckoutSlug, Agent: defaultAgentName},
	); !errors.Is(
		err,
		state.ErrStateNotFound,
	) {
		t.Errorf("dry run must not create the checkout-keyed state; got %v", err)
	}
}

// TestVolumeOpAppliesLegacyIdentity closes the apply-path gap: a non-dry-run
// volume op on a legacy layout must move the state onto the checkout key and do
// its work under that key. It observes the moved key both as the state
// directory and as the runner image the op ensured.
func TestVolumeOpAppliesLegacyIdentity(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)

	cmd, _ := setupCommandFixtures(t, cmdVolume, cmdReset)
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	// Capture the runner image the op ensured, to observe the key the op worked
	// under.
	var inspectedTag string
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			inspectedTag = ref
			return client.ImageInspectResult{Config: &dockerspec.DockerOCIImageConfig{}}, nil
		},
	})

	repoKey := state.Key{Slug: identity.RepoSlug, Agent: defaultAgentName}
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: defaultAgentName}
	if err := seedHomeState(repoKey, state.HomeState{
		HomeVolume: fmt.Sprintf("agents-sandbox-home-%s-20260101T000000", identity.RepoSlug),
	}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("volume reset: %v", err)
	}

	if want := naming.DockerImageRef(identity.CheckoutSlug, defaultAgentName); inspectedTag != want {
		t.Errorf("ensured runner image = %q, want the moved checkout-slug tag %q", inspectedTag, want)
	}
	if _, err := state.ReadState(checkoutKey); err != nil {
		t.Errorf("apply must leave the state under the checkout key: %v", err)
	}
	if _, err := state.ReadState(repoKey); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("apply must move the legacy state away; got %v", err)
	}
}

// TestVolumeOpAdoptionFailureFailsClosed pins the fail-closed adoption wiring:
// an unreadable legacy state aborts the volume op instead of being interpreted,
// before it ensures an image or touches a volume.
func TestVolumeOpAdoptionFailureFailsClosed(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)

	cmd, _ := setupCommandFixtures(t, cmdVolume, cmdReset)
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	writeUnparsableLegacyState(t, state.Key{Slug: identity.RepoSlug, Agent: defaultAgentName})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "check for a pending legacy identity") {
		t.Fatalf("volume reset with an unreadable legacy state: error = %v, want the fail-closed predicate error", err)
	}
}

// TestVolumeOpRecordsBakedAgentVersion pins that a volume op is a writer of
// record for the project's agent-version baseline: whatever release the ensured
// image bakes is recorded, so the spec's "whatever they bake is recorded" holds
// even though these ops resolve latest rather than the recorded baseline.
func TestVolumeOpRecordsBakedAgentVersion(t *testing.T) {
	initTestProjectDir(t)
	cmd, _ := setupCommandFixtures(t, cmdVolume, cmdReset)

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{
						"org.agents-sandbox.identity.agent-version": "3.3.3",
					},
				},
			}, nil
		},
	})

	checkoutKey := state.Key{Slug: git.MustCheckoutSlug(), Agent: defaultAgentName}
	if err := seedHomeState(checkoutKey, state.HomeState{HomeVolume: "home-vol"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("volume reset: %v", err)
	}

	st, err := state.ReadState(checkoutKey)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.AgentVersion != "3.3.3" {
		t.Errorf("AgentVersion = %q, want the baked 3.3.3 recorded", st.AgentVersion)
	}
}

// TestVolumeOpWarnsWhenRecordingAgentVersionFails verifies that a failed
// baseline write only warns: recording must never fail the volume op. It drives
// prepareVolumeOp directly: the full command re-reads the same state file right
// after (volumeOp's ReadState and its final commit), so a directory at the state
// path would fail those steps too; the recording step under test is
// prepareVolumeOp's.
func TestVolumeOpWarnsWhenRecordingAgentVersionFails(t *testing.T) {
	initTestProjectDir(t)
	root, ui := setupCommandFixtures(t, cmdVolume, cmdReset)
	cmd, _, err := root.Find([]string{cmdVolume, cmdReset})
	if err != nil {
		t.Fatalf("find volume reset: %v", err)
	}
	cmd.SetContext(context.Background())

	checkoutKey := state.Key{Slug: git.MustCheckoutSlug(), Agent: defaultAgentName}
	// A directory at the state file's path makes the baseline write fail.
	if err := os.MkdirAll(filepath.Join(state.KeyDir(checkoutKey), "state.yaml"), 0o700); err != nil {
		t.Fatalf("make state path unwritable: %v", err)
	}

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{
						"org.agents-sandbox.identity.agent-version": "3.3.3",
					},
				},
			}, nil
		},
	})

	if _, _, err := prepareVolumeOp(cmd, ui, false); err != nil {
		t.Fatalf("prepareVolumeOp must not fail on a state write error: %v", err)
	}
	assertWarnContains(t, ui, "could not persist the agent version")
}

func TestVolumeMigrateUnknownAgent(t *testing.T) {
	initTestProjectDir(t)
	cmd, _ := setupCommandFixtures(t, "volume", "migrate", "--agent", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}

func TestVolumeMigrateWithPositionalArg(t *testing.T) {
	initTestProjectDir(t)
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	doctor.MockedCheckAll(t, true)
	image.WithMockAgentVersion(t, "0.0.0-test")
	docker.WithNoopDockerMock(t)
	slug := git.MustCheckoutSlug()
	seedHomeState(state.Key{Slug: slug, Agent: defaultAgentName}, state.HomeState{
		HomeVolume: fmt.Sprintf("agents-sandbox-home-%s-20260101T000000", slug),
	})
	msb.WithMsbMock(t, mock)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"volume", "migrate", "explicit-vol-name"})
	if err := root.Execute(); err != nil {
		t.Fatalf("volume migrate with positional arg: %v", err)
	}
}

func TestBuildCommandOpenCodeVersionFallback(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "build", "--opencode-version", "1.2.3", "--dry-run")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build --opencode-version --dry-run: %v", err)
	}
}

func TestBuildCommandUnknownAgent(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "build", "--agent", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}

func TestBuildDockerfileUnknownAgent(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "build", "dockerfile", "--agent", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}

// errWriter returns the configured error on every write.
type errWriter struct{ err error }

func (w errWriter) Write(_ []byte) (int, error) { return 0, w.err }

// errStdOutUI embeds termio.Mock but returns an erroring StdOut writer.
type errStdOutUI struct {
	termio.Mock

	err error
}

func (e *errStdOutUI) StdOut() io.Writer { return errWriter{e.err} }

func TestBuildDockerfileStdOutError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	image.WithMockAgentVersion(t, "0.0.0-test")
	ui := &errStdOutUI{err: errors.New("write failed")}

	root := buildRootCmd(ui)
	root.SetArgs([]string{"build", "dockerfile"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected StdOut write error, got %v", err)
	}
}

func TestImagePruneInvalidAge(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "image", "prune", "--age", "bogus", "--yes")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid age") {
		t.Fatalf("expected invalid age error, got %v", err)
	}
}

func TestSandboxPruneInvalidAge(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "sandbox", "prune", "--age", "bogus", "--yes")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid age") {
		t.Fatalf("expected invalid age error, got %v", err)
	}
}

func TestShellCommandInvalidSize(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "shell", "--tmp-size", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --tmp-size") {
		t.Fatalf("expected invalid --tmp-size error, got %v", err)
	}
}

func TestResolvePruneAgeFromResolver(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildPruneCmd(ui)
	ctx := context.WithValue(context.Background(), (*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{ManualPruneAge: 5 * time.Hour}))
	cmd.SetContext(ctx)

	d, err := resolvePruneAge(cmd)
	if err != nil {
		t.Fatalf("resolvePruneAge: %v", err)
	}
	if d != 5*time.Hour {
		t.Errorf("resolvePruneAge = %v, want 5h", d)
	}
}

func TestConfigAgentHomeManifestError(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "agent", "opencode")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "home:\n  ../escape:\n")

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error from an escaping home target")
	}
}

// TestRunFuncServeOnlyContextError drives runFunc directly with --serve-only
// and a failing preflight so it takes the serve-only context branch and then
// returns the preflight error from runFunc itself.
func TestRunFuncServeOnlyContextError(t *testing.T) {
	initTestProjectDir(t)
	configpaths.WithMockConfigPaths(t)
	doctor.MockedCheckAll(t, false)
	ui := &termio.Mock{}

	cmd := buildRunCmd(ui)
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set(flagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	fn := runFunc(ui)
	if err := fn(cmd, nil); err == nil || err.Error() != "preflight failed" {
		t.Fatalf("expected preflight failure, got %v", err)
	}
}

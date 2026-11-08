package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// volumeOpScenario describes how to run a volume command and what to expect.
type volumeOpScenario struct {
	name        string
	command     string
	withStopped bool
	withActive  bool
	writeState  bool
	// emptyHomeVolume writes state with no home_volume set.
	emptyHomeVolume bool
	wantErrPart     string

	// error injection switches.
	preflightFail   bool
	ensureImageErr  bool
	createVolumeErr bool
	prefillErr      bool
	copyErr         bool
	editCreateErr   bool
}

func runVolumeOpScenario(t *testing.T, tc volumeOpScenario) {
	t.Helper()
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	mock := &msb.MockMsbClient{}

	doctortest.MockedCheckAll(t, !tc.preflightFail)
	withMockAgentVersion(t, "0.0.0-test")
	if tc.ensureImageErr {
		dockertest.WithDefaultErrorDockerMock(t)
	} else {
		dockertest.WithNoopDockerMock(t)
	}

	if tc.createVolumeErr {
		mock.CreateVolumeFn = func(context.Context, string, msb.VolumeSpec) (msb.VolumeHandle, error) {
			return nil, errBoom
		}
	}
	if tc.prefillErr || tc.copyErr || tc.editCreateErr {
		// PrefillVolume is always the first CreateSandbox; migrate's
		// CopyVolume and edit's own sandbox are the second.
		calls := 0
		mock.CreateSandboxFn = func(_ context.Context, _ string, _ msb.SandboxSpec) (msb.Sandbox, error) {
			calls++
			switch {
			case tc.prefillErr:
				return nil, errBoom
			case tc.copyErr && calls == 2:
				return msb.NewMockSandbox(msb.SandboxOpts{ExecErr: errBoom}), nil
			case tc.editCreateErr && calls == 2:
				return nil, errBoom
			default:
				return &msb.MockSandbox{}, nil
			}
		}
	}

	slug := git.MustCheckoutSlug()
	if tc.withStopped {
		mock.Sandboxes = append(mock.Sandboxes, mkStoppedVM(slug))
	}
	if tc.withActive {
		mock.Sandboxes = append(mock.Sandboxes, mkActiveVM(slug))
	}
	msb.WithMsbMock(t, mock)

	if tc.writeState {
		homeVolume := fmt.Sprintf("agents-sandbox-home-%s-20260814T132349", slug)
		if tc.emptyHomeVolume {
			homeVolume = ""
		}
		seedHomeState(state.Key{Slug: slug, Agent: defaultAgentName}, state.HomeState{
			HomeVolume:  homeVolume,
			ImageDigest: "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12",
			EnvState:    state.EnvState{},
			SecretState: state.SecretState{},
		})
	}

	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, tc.command})
	err := root.Execute()
	if tc.wantErrPart == "" {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Errorf("expected error containing %q, got none", tc.wantErrPart)
		return
	}
	if !strings.Contains(err.Error(), tc.wantErrPart) {
		t.Errorf("expected error containing %q, got: %v", tc.wantErrPart, err)
	}
}

func TestVolumeReset(t *testing.T) {
	for _, tc := range []volumeOpScenario{
		{name: "without VM", command: cmdReset, writeState: true},
		{name: "with stopped VM", command: cmdReset, withStopped: true, writeState: true},
		{name: "with active VM", command: cmdReset, withActive: true, writeState: true, wantErrPart: "VM still running"},
		{name: "no state", command: cmdReset, wantErrPart: "no state file found for project"},
		{name: "preflight failed", command: cmdReset, writeState: true, preflightFail: true, wantErrPart: "preflight failed"},
		{name: "ensure image error", command: cmdReset, writeState: true, ensureImageErr: true, wantErrPart: "ensure image"},
		{name: "no home volume in state", command: cmdReset, writeState: true, emptyHomeVolume: true, wantErrPart: "no volume to operate on"},
		{name: "create volume error", command: cmdReset, writeState: true, createVolumeErr: true, wantErrPart: "create volume"},
		{name: "prefill volume error", command: cmdReset, writeState: true, prefillErr: true, wantErrPart: "prefill new volume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runVolumeOpScenario(t, tc)
		})
	}
}

func TestVolumeMigrate(t *testing.T) {
	for _, tc := range []volumeOpScenario{
		{name: "without VM", command: cmdMigrate, writeState: true},
		{name: "with stopped VM", command: cmdMigrate, withStopped: true, writeState: true},
		{name: "with active VM", command: cmdMigrate, withActive: true, writeState: true, wantErrPart: "VM still running"},
		{name: "no state", command: cmdMigrate, wantErrPart: "no state file found for project"},
		{name: "preflight failed", command: cmdMigrate, writeState: true, preflightFail: true, wantErrPart: "preflight failed"},
		{name: "ensure image error", command: cmdMigrate, writeState: true, ensureImageErr: true, wantErrPart: "ensure image"},
		{name: "no home volume in state", command: cmdMigrate, writeState: true, emptyHomeVolume: true, wantErrPart: "no volume to operate on"},
		{name: "create volume error", command: cmdMigrate, writeState: true, createVolumeErr: true, wantErrPart: "create volume"},
		{name: "prefill volume error", command: cmdMigrate, writeState: true, prefillErr: true, wantErrPart: "prefill new volume"},
		{name: "copy volume error", command: cmdMigrate, writeState: true, copyErr: true, wantErrPart: "copy files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runVolumeOpScenario(t, tc)
		})
	}
}

// TestVolumeOpsDryRunDoesNotEnsureImage pins the dry-run contract of the volume
// ops: the preview message needs only the state-derived old volume, so the dry
// run must not build (or even inspect for) the runner image, and must not create
// a volume. Docker is mocked so that any image ensure fails the test loudly.
func TestVolumeOpsDryRunDoesNotEnsureImage(t *testing.T) {
	for _, command := range []string{cmdMigrate, cmdReset, cmdEdit} {
		t.Run(command, func(t *testing.T) {
			initTestProjectDir(t)
			configpathstest.WithMockConfigPaths(t)
			ui := &termiotest.Mock{}

			dockerMock := &dockertest.MockDockerClient{
				ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, errors.New("ImageInspect must not be called during --dry-run")
				},
				ImageBuildFn: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
					return client.ImageBuildResult{}, errors.New("ImageBuild must not be called during --dry-run")
				},
			}
			dockertest.WithDockerMock(t, dockerMock)
			doctortest.MockedCheckAll(t, true)
			withMockAgentVersion(t, "0.0.0-test")

			slug := git.MustCheckoutSlug()
			homeVolume := fmt.Sprintf("agents-sandbox-home-%s-20260814T132349", slug)
			if err := seedHomeState(state.Key{Slug: slug, Agent: defaultAgentName}, state.HomeState{
				HomeVolume: homeVolume,
			}); err != nil {
				t.Fatalf("seed home state: %v", err)
			}

			createVolumeCalls := 0
			msb.WithMsbMock(t, &msb.MockMsbClient{
				CreateVolumeFn: func(context.Context, string, msb.VolumeSpec) (msb.VolumeHandle, error) {
					createVolumeCalls++
					return nil, errors.New("CreateVolume must not be called during --dry-run")
				},
			})

			root := buildRootCmd(ui)
			root.SetArgs([]string{cmdVolume, command, "--dry-run"})
			if err := root.Execute(); err != nil {
				t.Fatalf("volume %s --dry-run: %v", command, err)
			}
			if createVolumeCalls != 0 {
				t.Errorf("CreateVolume called %d times during --dry-run, want 0", createVolumeCalls)
			}
			assertInfoHasPrefix(t, ui, "dry-run: Would ")
			for _, call := range ui.InfoCalls {
				if !strings.Contains(call, homeVolume) {
					t.Errorf("dry-run message %q does not name the old volume %q", call, homeVolume)
				}
			}
		})
	}
}

func TestVolumeEdit(t *testing.T) {
	for _, tc := range []volumeOpScenario{
		{name: "without VM", command: cmdEdit, writeState: true},
		{name: "with stopped VM", command: cmdEdit, withStopped: true, writeState: true},
		{name: "with active VM", command: cmdEdit, withActive: true, writeState: true, wantErrPart: "VM still running"},
		{name: "no state", command: cmdEdit, wantErrPart: "no state file found for project"},
		{name: "preflight failed", command: cmdEdit, writeState: true, preflightFail: true, wantErrPart: "preflight failed"},
		{name: "ensure image error", command: cmdEdit, writeState: true, ensureImageErr: true, wantErrPart: "ensure image"},
		{name: "no home volume in state", command: cmdEdit, writeState: true, emptyHomeVolume: true, wantErrPart: "no volume to operate on"},
		{name: "create volume error", command: cmdEdit, writeState: true, createVolumeErr: true, wantErrPart: "create volume"},
		{name: "prefill volume error", command: cmdEdit, writeState: true, prefillErr: true, wantErrPart: "prefill new volume"},
		{name: "edit sandbox error", command: cmdEdit, writeState: true, editCreateErr: true, wantErrPart: "create edit sandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runVolumeOpScenario(t, tc)
		})
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
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
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

// TestVolumeOpAppliesLegacyIdentity pins that a non-dry-run volume op on a
// legacy layout moves the state onto the checkout key and does its work under
// that key. It observes the moved key both as the state
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
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
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

	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
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

	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
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
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	mock := &msb.MockMsbClient{}
	doctortest.MockedCheckAll(t, true)
	withMockAgentVersion(t, "0.0.0-test")
	dockertest.WithNoopDockerMock(t)
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

// TestBuildVolumeOpsRejectsTwoArgs pins the volume-op arity bound: at most one
// positional volume name.
func TestBuildVolumeOpsRejectsTwoArgs(t *testing.T) {
	cmd := buildVolumeOpsCmd(&termiotest.Mock{}, volumeOps[0])
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("expected an error for two positional args")
	}
	if err := cmd.Args(cmd, []string{"a"}); err != nil {
		t.Errorf("one positional arg should be accepted, got %v", err)
	}
}

// TestBuildVolumeOpsForwardsPositionalNameAndInfo pins that the positional name
// reaches the op and the ensured image's info is passed through, and that the
// --remove flag is registered from the op definition.
func TestBuildVolumeOpsForwardsPositionalNameAndInfo(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	doctortest.MockedCheckAll(t, true)
	withMockAgentVersion(t, "3.3.3")
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{"org.agents-sandbox.identity.agent-version": "3.3.3"},
				},
			}, nil
		},
	})
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	var gotName string
	var gotInfo image.ImageInfo
	op := volumeOp{
		name:       "probe",
		removeHelp: "remove probe volume",
		run: func(_ context.Context, _ docker.Client, _ state.Key, name string, info image.ImageInfo, _, _ bool, _ termio.UI) error {
			gotName = name
			gotInfo = info
			return nil
		},
	}
	cmd := buildVolumeOpsCmd(&termiotest.Mock{}, op)
	cmd.SetArgs([]string{"explicit-vol-name"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotName != "explicit-vol-name" {
		t.Errorf("volumeName = %q, want %q", gotName, "explicit-vol-name")
	}
	if gotInfo.AgentVersion != "3.3.3" {
		t.Errorf("info.AgentVersion = %q, want %q", gotInfo.AgentVersion, "3.3.3")
	}
	if f := cmd.Flags().Lookup(flagRemove); f == nil || f.Usage != op.removeHelp {
		t.Errorf("--%s flag = %v, want usage %q", flagRemove, f, op.removeHelp)
	}
}

// TestVolumeOpsEnsureImageErrorWrapsCause pins that an ensure-image failure is
// wrapped with %w rather than %v.
func TestVolumeOpsEnsureImageErrorWrapsCause(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	doctortest.MockedCheckAll(t, true)
	withMockAgentVersion(t, "0.0.0-test")
	dockertest.WithDefaultErrorDockerMock(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	cmd := buildVolumeOpsCmd(&termiotest.Mock{}, volumeOps[0])
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an ensure-image error")
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %v is not wrapped with %%w", err)
	}
}

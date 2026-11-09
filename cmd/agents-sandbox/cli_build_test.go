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

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestBuildCommand(t *testing.T) {
	for _, commands := range [][]string{
		{cmdBuild},
		{cmdImage, cmdBuild},
	} {
		t.Run(
			fmt.Sprintf("%s --dry-run → info 'dry-run: Would build runner image'", strings.Join(commands, " ")),
			func(t *testing.T) {
				cmd, ui := setupCommandFixtures(t, append(commands, "--dry-run")...)

				if err := cmd.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if !slices.Contains(ui.InfoCalls, "dry-run: Would build runner image") {
					t.Errorf("expected info 'dry-run: Would build runner image'; got: %v", ui.InfoCalls)
				}

				// Dry-run must not invoke docker at all
				if len(ui.SpinnerCalls) > 0 {
					t.Errorf("dry-run should not spawn spinner; got: %v", ui.SpinnerCalls)
				}
			},
		)

		t.Run(
			fmt.Sprintf(
				"%s --dry-run --rebuild → info 'dry-run: Would build runner image'",
				strings.Join(commands, " "),
			),
			func(t *testing.T) {
				cmd, ui := setupCommandFixtures(t, append(commands, "--dry-run", "--rebuild")...)

				if err := cmd.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if !slices.Contains(ui.InfoCalls, "dry-run: Would build runner image") {
					t.Errorf("expected info 'dry-run: Would build runner image'; got: %v", ui.InfoCalls)
				}

				// --rebuild is a no-op in dry-run; no spinner should be started
				if len(ui.SpinnerCalls) > 0 {
					t.Errorf("dry-run should not spawn spinner even with --rebuild; got: %v", ui.SpinnerCalls)
				}
			},
		)

		t.Run(
			fmt.Sprintf(
				"%s (no flags) with docker client error → non-nil error (docker preflight fails first)",
				strings.Join(commands, " "),
			),
			func(t *testing.T) {
				cmd, ui := setupCommandFixtures(t, commands...)
				dockertest.WithDefaultErrorDockerMock(t)

				err := cmd.Execute()

				if err == nil {
					t.Error("expected non-nil error from build image failure")
				}
				// With a broken docker client, the docker preflight (Ping) fails
				// before the build spinner is started; base-image resolution is
				// never reached.
				if slices.Contains(ui.SpinnerCalls, "Ensuring runner image") {
					t.Errorf(
						"expected no 'Ensuring runner image' spinner when the docker preflight fails first; got: %v",
						ui.SpinnerCalls,
					)
				}
			},
		)

		t.Run(fmt.Sprintf(
			"%s --dry-run → same behavior as build --dry-run (shared buildBuildCmd)",
			strings.Join(commands, " "),
		), func(t *testing.T) {
			cmd, ui := setupCommandFixtures(t, append(commands, "--dry-run")...)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !slices.Contains(ui.InfoCalls, "dry-run: Would build runner image") {
				t.Errorf("expected info 'dry-run: Would build runner image' via image build; got: %v", ui.InfoCalls)
			}
		})

		t.Run(fmt.Sprintf(
			"%s with docker not available → error 'docker not available'",
			strings.Join(commands, " "),
		), func(t *testing.T) {
			cmd, _ := setupCommandFixtures(t, commands...)
			doctortest.MockedCheckDocker(t, false)

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error, got none")
			}
			if !strings.Contains(err.Error(), "docker not available") {
				t.Errorf("expected 'docker not available'; got: %v", err)
			}
		})
	}
}

func TestBuildCommandHasDockerFlags(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdBuild, "--help")
	foundCmd, _, err := cmd.Find([]string{cmdBuild})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdBuild, err)
	}
	if flag := foundCmd.Flags().Lookup(flagDocker); flag == nil {
		t.Error("build command must have --docker flag")
	}
	if flag := foundCmd.Flags().Lookup(flagDind); flag == nil {
		t.Error("build command must keep the deprecated --dind alias")
	}
}

func TestBuildCommandHasAgentVersionFlag(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdBuild, "--help")
	foundCmd, _, err := cmd.Find([]string{cmdBuild})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdBuild, err)
	}
	agentVersion := foundCmd.Flags().Lookup(flagAgentVersion)
	if agentVersion == nil {
		t.Fatal("build command must have --agent-version flag")
	}
	if agentVersion.Name != "agent-version" {
		t.Errorf("flag name = %q, want %q", agentVersion.Name, "agent-version")
	}
	openCodeVersion := foundCmd.Flags().Lookup(flagOpenCodeVersion)
	if openCodeVersion == nil {
		t.Fatal("build command must keep --opencode-version as a deprecated alias")
	}
	if openCodeVersion.Name != "opencode-version" {
		t.Errorf("flag name = %q, want %q", openCodeVersion.Name, "opencode-version")
	}
}

// TestBuildAgentVersionPrecedenceMatchesRun pins that build shares run's
// agent-version precedence: a user-provided agent is not owned by the tool, so
// --agent-version is ignored and the recorded version is baked, while a
// tool-managed project honors the pin. It also pins that build forwards the
// user-provided signal to the image layer: with no recorded version the build
// keeps a stable user-provided identity and defers release resolution past the
// build decision instead of resolving one up front.
func TestBuildAgentVersionPrecedenceMatchesRun(t *testing.T) {
	tests := []struct {
		name            string
		agentSource     string
		recordedVersion string
		pin             string
		wantVersion     string
		// wantDeferred pins whether release resolution must wait for the build
		// decision: it may only be deferred when the user-provided signal
		// reaches the image layer.
		wantDeferred bool
	}{
		{
			name:            "user-provided agent ignores the pin",
			agentSource:     "user",
			recordedVersion: "1.0.0",
			pin:             "9.9.9",
			wantVersion:     "1.0.0",
		},
		{
			name:            "user-provided agent with no recorded version defers resolution",
			agentSource:     "user",
			recordedVersion: "",
			pin:             "9.9.9",
			wantVersion:     "",
			wantDeferred:    true,
		},
		{
			name:            "tool-managed agent honors the pin",
			agentSource:     "tool",
			recordedVersion: "1.0.0",
			pin:             "9.9.9",
			wantVersion:     "9.9.9",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			initTestProjectDir(t)
			cmd, _ := setupCommandFixtures(t, cmdBuild, "--agent-version", testCase.pin)

			var resolvedVersion string
			var resolverCalled bool
			var buildDecisionRan bool
			var resolvedBeforeBuildDecision bool
			withMockAgentVersionResolver(
				t,
				func(_ context.Context, _ agent.Agent, requested string) (string, error) {
					resolvedVersion = requested
					if !resolverCalled {
						resolvedBeforeBuildDecision = !buildDecisionRan
					}
					resolverCalled = true
					return "0.0.0-test", nil
				},
			)
			// The build decision inspects the Docker image before deciding
			// whether a rebuild is needed. Recording that inspect lets the test
			// tell a deferred resolution (which only runs once the decision
			// needs a rebuild) from resolution that happens up front.
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
				ImageInspectFn: func(
					_ context.Context, _ string, _ ...client.ImageInspectOption,
				) (client.ImageInspectResult, error) {
					buildDecisionRan = true
					return client.ImageInspectResult{
						Config: &dockerspec.DockerOCIImageConfig{},
					}, nil
				},
			})

			key := state.Key{Slug: git.MustCheckoutSlug(), Agent: defaultAgentName}
			if err := seedHomeState(key, state.HomeState{
				AgentVersion: testCase.recordedVersion,
				AgentSource:  testCase.agentSource,
			}); err != nil {
				t.Fatalf("seed state: %v", err)
			}

			if err := cmd.Execute(); err != nil {
				t.Fatalf("build: %v", err)
			}
			if !resolverCalled {
				t.Fatal("build must resolve an agent version")
			}
			if resolvedVersion != testCase.wantVersion {
				t.Errorf("build resolved agent version %q, want %q", resolvedVersion, testCase.wantVersion)
			}
			if deferred := !resolvedBeforeBuildDecision; deferred != testCase.wantDeferred {
				t.Errorf(
					"deferred resolution = %v, want %v: build must forward the user-provided signal to the image layer",
					deferred, testCase.wantDeferred,
				)
			}
		})
	}
}

// TestBuildCommandRecordsPinnedAgentVersion pins that the standalone build is a
// writer of record for the project's agent-version baseline: the release the
// built image reports is recorded, so an explicit --agent-version pin survives
// later unpinned runs.
func TestBuildCommandRecordsPinnedAgentVersion(t *testing.T) {
	initTestProjectDir(t)
	cmd, _ := setupCommandFixtures(t, cmdBuild, "--agent-version", "9.9.9")

	// The mocked image label and the requested --agent-version pin are both
	// 9.9.9, so this test cannot distinguish which supplied the recorded value —
	// only that it is not the fixture's freshly resolved 0.0.0-test. No label
	// matches the dockerfile-id, so the build genuinely runs and inspects.
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{
						"org.agents-sandbox.identity.agent-version": "9.9.9",
					},
				},
			}, nil
		},
	})

	key := state.Key{Slug: git.MustCheckoutSlug(), Agent: defaultAgentName}
	if err := seedHomeState(key, state.HomeState{HomeVolume: "home-vol"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("build: %v", err)
	}

	st, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.AgentVersion != "9.9.9" {
		t.Errorf("AgentVersion = %q, want the pinned 9.9.9 recorded", st.AgentVersion)
	}
}

// TestBuildCommandWarnsWhenRecordingAgentVersionFails verifies that a failed
// baseline write only warns: recording must never fail a build.
func TestBuildCommandWarnsWhenRecordingAgentVersionFails(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdBuild, "--agent-version", "9.9.9")

	// A directory at the state file's path makes the baseline write fail.
	key := state.Key{Slug: git.MustCheckoutSlug(), Agent: defaultAgentName}
	if err := os.MkdirAll(filepath.Join(state.KeyDir(key), "state.yaml"), 0o700); err != nil {
		t.Fatalf("make state path unwritable: %v", err)
	}

	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{
						"org.agents-sandbox.identity.agent-version": "9.9.9",
					},
				},
			}, nil
		},
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("build must not fail on a state write error: %v", err)
	}
	assertWarnContains(t, ui, "could not persist the agent version")
}

func TestRunAndShellHaveAgentVersionFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		t.Run(name, func(t *testing.T) {
			cmd, _ := setupCommandFixtures(t, name, "--help")
			foundCmd, _, err := cmd.Find([]string{name})
			if err != nil {
				t.Fatalf("Find %q: %v", name, err)
			}
			if flag := foundCmd.Flags().Lookup(flagAgentVersion); flag == nil {
				t.Errorf("%s command must have --agent-version flag", name)
			}
		})
	}
}

func TestBuildDockerfileCommand(t *testing.T) {
	for _, commands := range [][]string{
		{cmdBuild, cmdDockerfile},
		{cmdImage, cmdBuild, cmdDockerfile},
	} {
		t.Run(
			fmt.Sprintf("%s prints the rendered Dockerfile", strings.Join(commands, " ")),
			func(t *testing.T) {
				cmd, ui := setupCommandFixtures(t, commands...)

				if err := cmd.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				out := ui.StdOutBuffer.String()
				for _, want := range []string{
					"FROM docker.io/library/debian:trixie-slim",
					"LABEL org.agents-sandbox.agent=opencode2",
					"USER dev",
					"WORKDIR /workspace",
				} {
					if !strings.Contains(out, want) {
						t.Errorf("dockerfile output missing %q; got:\n%s", want, out)
					}
				}
				// No --docker flag: the docker stage must be absent.
				if strings.Contains(out, "agents-sandbox-docker") {
					t.Errorf("dockerfile output must not contain the docker stage without --docker; got:\n%s", out)
				}
			},
		)

		t.Run(
			fmt.Sprintf("%s --docker adds the docker stage", strings.Join(commands, " ")),
			func(t *testing.T) {
				cmd, ui := setupCommandFixtures(t, append(commands, "--docker")...)

				if err := cmd.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				out := ui.StdOutBuffer.String()
				for _, want := range []string{"agents-sandbox-docker", "DOCKER_VERSION"} {
					if !strings.Contains(out, want) {
						t.Errorf("dockerfile output with --docker missing %q; got:\n%s", want, out)
					}
				}
			},
		)

		t.Run(
			fmt.Sprintf("%s --dind (deprecated) adds the docker stage", strings.Join(commands, " ")),
			func(t *testing.T) {
				cmd, ui := setupCommandFixtures(t, append(commands, "--dind")...)

				if err := cmd.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				out := ui.StdOutBuffer.String()
				if !strings.Contains(out, "agents-sandbox-docker") {
					t.Errorf("dockerfile output with deprecated --dind must contain the docker stage; got:\n%s", out)
				}
			},
		)

		t.Run(
			fmt.Sprintf("%s does not invoke docker", strings.Join(commands, " ")),
			func(t *testing.T) {
				cmd, ui := setupCommandFixtures(t, commands...)

				if err := cmd.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if len(ui.SpinnerCalls) > 0 {
					t.Errorf("dockerfile should not spawn a spinner; got: %v", ui.SpinnerCalls)
				}
			},
		)
	}
}

func TestBuildDockerfileCommandHasDockerFlags(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdBuild, "--help")
	foundCmd, _, err := cmd.Find([]string{cmdBuild, cmdDockerfile})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdDockerfile, err)
	}
	if flag := foundCmd.Flags().Lookup(flagDocker); flag == nil {
		t.Error("build dockerfile command must have --docker flag")
	}
	if flag := foundCmd.Flags().Lookup(flagDind); flag == nil {
		t.Error("build dockerfile command must keep the deprecated --dind alias")
	}
	if flag := foundCmd.Flags().Lookup(flagAgent); flag == nil {
		t.Error("build dockerfile command must have --agent flag")
	}
	if flag := foundCmd.Flags().Lookup(flagAgentVersion); flag == nil {
		t.Error("build dockerfile command must have --agent-version flag")
	}
}

// TestBuildDockerfileCommandRendersAgentVersion verifies the preview carries a
// pinned --agent-version into the rendered agent stage, so the printed
// Dockerfile reflects the version that would be baked.
func TestBuildDockerfileCommandRendersAgentVersion(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdBuild, cmdDockerfile, "--agent-version", "9.9.9")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out := ui.StdOutBuffer.String(); !strings.Contains(out, "ARG OPENCODE2_VERSION=9.9.9") {
		t.Errorf("dockerfile output must pin the agent version; got:\n%s", out)
	}
}

// TestBuildDockerfileCommandReportsReservedAlias verifies a project Dockerfile
// declaring a reserved stage alias makes `build dockerfile` fail instead of
// printing a broken Dockerfile.
func TestBuildDockerfileCommandReportsReservedAlias(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdBuild, cmdDockerfile)
	if err := os.WriteFile(
		configpaths.Get().ProjectDockerfile(),
		[]byte("FROM debian:trixie-slim AS agents-sandbox-base\n"),
		0o644,
	); err != nil {
		t.Fatalf("write project dockerfile: %v", err)
	}
	approveProjectConfig(t)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "reserved stage alias") {
		t.Fatalf("Execute error = %v, want a reserved-stage-alias error", err)
	}
}

// TestResolveBuildDockerSetting pins the build's docker/dind precedence, the
// same one the run path uses: --docker beats the deprecated --dind, either flag
// beats the config, and a config value is explicit too, so a `docker: false`
// overrides a project-Dockerfile content signal. Without any source docker is
// off and not explicit.
func TestResolveBuildDockerSetting(t *testing.T) {
	cases := []struct {
		name   string
		flags  map[string]string
		config map[string]any // user config.yaml; nil means no resolver
		want   options.DockerSetting
	}{
		{name: "no source", want: options.DockerSetting{}},
		{
			name:  "--docker",
			flags: map[string]string{flagDocker: "true"},
			want:  options.DockerSetting{Enabled: true, Explicit: true},
		},
		{
			name:  "deprecated --dind",
			flags: map[string]string{flagDind: "true"},
			want:  options.DockerSetting{Enabled: true, Explicit: true},
		},
		{
			name:  "--docker beats --dind",
			flags: map[string]string{flagDind: "true", flagDocker: "false"},
			want:  options.DockerSetting{Explicit: true},
		},
		{
			name:   "config on",
			config: map[string]any{"docker": true},
			want:   options.DockerSetting{Enabled: true, Explicit: true},
		},
		{name: "config off", config: map[string]any{"docker": false}, want: options.DockerSetting{Explicit: true}},
		{
			name:   "config off beats --dind",
			flags:  map[string]string{flagDind: "true"},
			config: map[string]any{"docker": false},
			want:   options.DockerSetting{Explicit: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			cmd := buildDockerfileCmd(&termiotest.Mock{})
			for name, value := range tc.flags {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatalf("set %s: %v", name, err)
				}
			}
			if tc.config != nil {
				testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", tc.config)
				cmd.SetContext(context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd)))
			}

			if got := resolveBuildDockerSetting(cmd); got != tc.want {
				t.Errorf("resolveBuildDockerSetting = %+v, want %+v", got, tc.want)
			}
		})
	}
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
	mock := &msbtest.MockMsbClient{}
	msbtest.WithMsbMock(t, mock)
	// The default noop Docker mock reports no image ID, which would fall back to
	// the name ref. Return a real ID so the build exercises the content ref.
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
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
	mock := &msbtest.MockMsbClient{}
	msbtest.WithMsbMock(t, mock)

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
	doctortest.MockedCheckDocker(t, false)

	// A running legacy VM that adoption would destroy if it ran first.
	legacyVM := &msbtest.MockSandboxHandle{
		Name_:   naming.LegacyVMPrefix + identity.RepoSlug + "-" + defaultAgentName,
		Status_: msb.SandboxStatusRunning,
	}
	mock := &msbtest.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return legacyVM, nil
		},
	}
	msbtest.WithMsbMock(t, mock)

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

// TestResolveBuildAgentVersion pins the --agent-version/--opencode-version
// precedence: --agent-version wins and the deprecated alias only applies when
// --agent-version was not given.
func TestResolveBuildAgentVersion(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"none", nil, ""},
		{"agent-version", []string{"--agent-version", "1.2.3"}, "1.2.3"},
		{"opencode-version fallback", []string{"--opencode-version", "9.9.9"}, "9.9.9"},
		{"agent-version wins", []string{"--agent-version", "1.2.3", "--opencode-version", "9.9.9"}, "1.2.3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := buildBuildCmd(&termiotest.Mock{})
			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatalf("parse %v: %v", tc.args, err)
			}
			if got := resolveBuildAgentVersion(cmd); got != tc.want {
				t.Errorf("resolveBuildAgentVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildDockerCheckErrorWrapsCause pins that a failing Docker prerequisite is
// wrapped with %w (so the cause stays reachable) rather than %v.
func TestBuildDockerCheckErrorWrapsCause(t *testing.T) {
	initTestProjectDir(t)
	cmd, _ := setupCommandFixtures(t, cmdBuild)
	doctortest.MockedCheckDocker(t, false)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a docker prerequisite error")
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %v is not wrapped with %%w", err)
	}
}

// TestBuildAdoptionFailureFailsClosed pins that a build whose legacy-identity
// adoption fails stops with the adoption wrapper instead of proceeding against
// a zero identity.
func TestBuildAdoptionFailureFailsClosed(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)

	cmd, _ := setupCommandFixtures(t, cmdBuild)
	msbtest.WithMsbMock(t, &msbtest.MockMsbClient{})
	writeUnparsableLegacyState(t, state.Key{Slug: identity.RepoSlug, Agent: defaultAgentName})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "adopt legacy identity") {
		t.Fatalf("build with an unreadable legacy state: error = %v, want the adoption wrapper", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %v is not wrapped with %%w", err)
	}
}

// TestBuildCommandsRejectAnUnknownAgent pins that `build` and `build
// dockerfile` refuse an agent name that is not registered.
func TestBuildCommandsRejectAnUnknownAgent(t *testing.T) {
	for _, args := range [][]string{{"build", "--agent", "bogus"}, {"build", "dockerfile", "--agent", "bogus"}} {
		t.Run(strings.Join(args[:len(args)-2], " "), func(t *testing.T) {
			cmd, _ := setupCommandFixtures(t, args...)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), `unknown agent "bogus"`) {
				t.Fatalf("error = %v, want the unknown agent refused", err)
			}
		})
	}
}

func TestBuildDockerfileStdOutError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	withMockAgentVersion(t, "0.0.0-test")
	ui := &errStdOutUI{err: errors.New("write failed")}

	root := buildRootCmd(ui)
	root.SetArgs([]string{"build", "dockerfile"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected StdOut write error, got %v", err)
	}
}

// errWriter returns the configured error on every write.
type errWriter struct{ err error }

func (w errWriter) Write(_ []byte) (int, error) { return 0, w.err }

// errStdOutUI embeds termiotest.Mock but returns an erroring StdOut writer.
type errStdOutUI struct {
	termiotest.Mock

	err error
}

func (e *errStdOutUI) StdOut() io.Writer { return errWriter{e.err} }

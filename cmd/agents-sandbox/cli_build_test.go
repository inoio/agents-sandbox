package main

import (
	"context"
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
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
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
				docker.WithDefaultErrorDockerMock(t)

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
			doctor.MockedCheckDocker(t, false)

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
			image.WithMockAgentVersionResolver(
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
			docker.WithDockerMock(t, &docker.MockDockerClient{
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
	docker.WithDockerMock(t, &docker.MockDockerClient{
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

	docker.WithDockerMock(t, &docker.MockDockerClient{
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

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "reserved stage alias") {
		t.Fatalf("Execute error = %v, want a reserved-stage-alias error", err)
	}
}

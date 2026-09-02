package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/sandbox/doctor"

	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
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

func TestBuildCommandHasDindFlag(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdBuild, "--help")
	foundCmd, _, err := cmd.Find([]string{cmdBuild})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdBuild, err)
	}
	if flag := foundCmd.Flags().Lookup(flagDind); flag == nil {
		t.Error("build command must have --dind flag")
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
					"FROM debian:trixie-slim",
					"LABEL org.opencode-sandbox.agent=opencode",
					"USER dev",
					"WORKDIR /workspace",
				} {
					if !strings.Contains(out, want) {
						t.Errorf("dockerfile output missing %q; got:\n%s", want, out)
					}
				}
				// No --dind flag: the dind block must be absent.
				if strings.Contains(out, "DOCKER_VERSION") {
					t.Errorf("dockerfile output must not contain dind block without --dind; got:\n%s", out)
				}
			},
		)

		t.Run(
			fmt.Sprintf("%s --dind appends the dind block", strings.Join(commands, " ")),
			func(t *testing.T) {
				cmd, ui := setupCommandFixtures(t, append(commands, "--dind")...)

				if err := cmd.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				out := ui.StdOutBuffer.String()
				if !strings.Contains(out, "DOCKER_VERSION") {
					t.Errorf("dockerfile output with --dind must contain the dind block; got:\n%s", out)
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

func TestBuildDockerfileCommandHasDindFlag(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdBuild, "--help")
	foundCmd, _, err := cmd.Find([]string{cmdBuild, cmdDockerfile})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdDockerfile, err)
	}
	if flag := foundCmd.Flags().Lookup(flagDind); flag == nil {
		t.Error("build dockerfile command must have --dind flag")
	}
	if flag := foundCmd.Flags().Lookup(flagAgent); flag == nil {
		t.Error("build dockerfile command must have --agent flag")
	}
}

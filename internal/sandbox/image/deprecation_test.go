package image

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestDiagnoseDockerfile verifies the classified result for one project
// Dockerfile: the effective capability and its source, plus one message per
// offending line. It covers the two content signals, config suppression, the
// earlier-stage marker, near-miss case handling, and the legacy deprecations.
func TestDiagnoseDockerfile(t *testing.T) {
	baseWarn := "project Dockerfile uses the deprecated agents-sandbox/runner-base-dind base image; switch to agents-sandbox/runner-base-docker"
	markerWarn := "project Dockerfile uses the deprecated # agents-sandbox:dind marker; switch to # agents-sandbox:docker"
	earlierWarn := "project Dockerfile has a docker marker outside the final stage; it is ignored — move it into the final stage (after the last FROM)"
	nearMissWarn := `project Dockerfile has a near-miss docker marker "# Agents-Sandbox:docker"; use "# agents-sandbox:docker"`
	dockerExplicitOff := options.DockerSetting{Enabled: false, Explicit: true}
	dockerExplicitOn := options.DockerSetting{Enabled: true, Explicit: true}
	cases := []struct {
		name    string
		project string
		setting options.DockerSetting
		enabled bool
		source  string
		want    []string
	}{
		{name: "empty", setting: dockerOff},
		{name: "plain body", project: "FROM debian:trixie-slim\nRUN echo hi\n", setting: dockerOff},
		{
			name: "marker enables", project: "FROM debian:trixie-slim\n# agents-sandbox:docker\nRUN echo hi\n",
			setting: dockerOff, enabled: true, source: "Dockerfile marker",
			want: []string{"docker: on (project Dockerfile marker)"},
		},
		{
			name: "managed base enables", project: "FROM agents-sandbox/runner-base-docker\nRUN echo hi\n",
			setting: dockerOff, enabled: true, source: "Dockerfile base",
			want: []string{"docker: on (project Dockerfile base)"},
		},
		{
			name:    "config suppresses marker",
			project: "FROM debian:trixie-slim\n# agents-sandbox:docker\nRUN echo hi\n",
			setting: dockerExplicitOff,
			enabled: false,
			source:  "config",
			want:    []string{"docker: off (config); project Dockerfile asks for docker"},
		},
		{
			name:    "config suppresses docker base",
			project: "FROM agents-sandbox/runner-base-docker\nRUN echo hi\n",
			setting: dockerExplicitOff,
			enabled: false,
			source:  "config",
			want:    []string{"docker: off (config); project Dockerfile asks for docker"},
		},
		{
			name:    "config suppresses dind base",
			project: "FROM agents-sandbox/runner-base-dind\nRUN echo hi\n",
			setting: dockerExplicitOff,
			enabled: false,
			source:  "config",
			want:    []string{"docker: off (config); project Dockerfile asks for docker", baseWarn},
		},
		{
			name:    "explicit on wins with content",
			project: "FROM debian:trixie-slim\n# agents-sandbox:docker\nRUN echo hi\n",
			setting: dockerExplicitOn,
			enabled: true,
			source:  "config",
		},
		{
			name:    "explicit off without content",
			project: "FROM debian:trixie-slim\nRUN echo hi\n",
			setting: dockerExplicitOff,
			enabled: false,
			source:  "config",
		},
		{
			name:    "earlier-stage marker ignored",
			project: "FROM debian:trixie-slim AS x\n# agents-sandbox:dind\nFROM debian:trixie-slim\nRUN echo hi\n",
			setting: dockerOff,
			want:    []string{earlierWarn},
		},
		{
			name: "dind base warns and enables", project: "FROM agents-sandbox/runner-base-dind\nRUN echo hi\n",
			setting: dockerOff, enabled: true, source: "Dockerfile base",
			want: []string{"docker: on (project Dockerfile base)", baseWarn},
		},
		{
			name:    "final legacy marker warns and enables",
			project: "FROM debian:trixie-slim\n# agents-sandbox:dind\nRUN echo hi\n",
			setting: dockerOff,
			enabled: true,
			source:  "Dockerfile marker",
			want:    []string{"docker: on (project Dockerfile marker)", markerWarn},
		},
		{
			name:    "earlier-stage marker does not stop the scan",
			project: "FROM debian:trixie-slim AS x\n# agents-sandbox:docker\nFROM debian:trixie-slim\n# agents-sandbox:dind\nRUN echo hi\n",
			setting: dockerOff,
			enabled: true,
			source:  "Dockerfile marker",
			want:    []string{"docker: on (project Dockerfile marker)", earlierWarn, markerWarn},
		},
		{
			name:    "final marker does not stop the scan",
			project: "FROM debian:trixie-slim\n# agents-sandbox:docker\n# Agents-Sandbox:docker\nRUN echo hi\n",
			setting: dockerOff,
			enabled: true,
			source:  "Dockerfile marker",
			want:    []string{"docker: on (project Dockerfile marker)", nearMissWarn},
		},
		{
			name:    "namespace without docker is not a near miss",
			project: "FROM debian:trixie-slim\n# agents-sandbox:network\nRUN echo hi\n",
			setting: dockerOff,
		},
		{
			name:    "backslash comment does not swallow the next marker",
			project: "FROM debian:trixie-slim\n# note \\\n# agents-sandbox:docker\nRUN echo hi\n",
			setting: dockerOff,
			enabled: true,
			source:  "Dockerfile marker",
			want:    []string{"docker: on (project Dockerfile marker)"},
		},
		{
			name:    "near miss warns",
			project: "FROM debian:trixie-slim\n# Agents-Sandbox:docker\nRUN echo hi\n",
			setting: dockerOff,
			enabled: false,
			want:    []string{nearMissWarn},
		},
		{
			name:    "heredoc marker is inert",
			project: "FROM debian:trixie-slim\nCOPY <<EOF /tmp/example.Dockerfile\n# agents-sandbox:docker\nEOF\nRUN echo hi\n",
			setting: dockerOff,
		},
		{
			name:    "heredoc near miss is inert",
			project: "FROM debian:trixie-slim\nCOPY <<EOF /tmp/example.Dockerfile\n# Agents-Sandbox:docker\nEOF\nRUN echo hi\n",
			setting: dockerOff,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiagnoseDockerfile([]byte(tc.project), tc.setting)
			if got.Enabled != tc.enabled || got.Source != tc.source {
				t.Errorf("Enabled/Source = %v/%q, want %v/%q", got.Enabled, got.Source, tc.enabled, tc.source)
			}
			if strings.Join(got.Messages, "|") != strings.Join(tc.want, "|") {
				t.Errorf("Messages = %q, want %q", got.Messages, tc.want)
			}
		})
	}
}

// TestNearMissDockerMarker pins the detection rule: a comment naming the
// sandbox namespace and docker/dind is a near miss only when it is not an exact
// marker, and the detection is case-insensitive even though enablement is not.
func TestNearMissDockerMarker(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{name: "exact current marker", line: dockerMarker, want: false},
		{name: "exact legacy marker", line: legacyDockerMarker, want: false},
		{name: "case variant", line: "# Agents-Sandbox:docker", want: true},
		{name: "legacy case variant", line: "# agents-sandbox:DIND", want: true},
		{name: "plain comment", line: "# just a comment", want: false},
		{name: "not a comment", line: "RUN agents-sandbox:docker", want: false},
		{name: "other namespace", line: "# other:docker", want: false},
		{name: "namespace without docker", line: "# agents-sandbox:network", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nearMissDockerMarker(tc.line); got != tc.want {
				t.Errorf("nearMissDockerMarker(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// TestEmitProjectDockerfileDiagnostics verifies the on-disk convenience reads
// the project Dockerfile and routes its diagnostics to the UI's warn channel.
func TestEmitProjectDockerfileDiagnostics(t *testing.T) {
	dir := t.TempDir()
	projectFile := filepath.Join(dir, configpaths.DockerFileName)
	if err := os.WriteFile(
		projectFile,
		[]byte("FROM debian:trixie-slim\n# agents-sandbox:dind\nRUN echo hi\n"),
		0o644,
	); err != nil {
		t.Fatalf("write project Dockerfile: %v", err)
	}
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return &projectDockerfilePaths{dir: dir} }
	t.Cleanup(func() { configpaths.Get = orig })

	ui := &termio.Mock{}
	EmitProjectDockerfileDiagnostics(ui, dockerOff)

	joined := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{"docker: on (project Dockerfile marker)", "# agents-sandbox:dind"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a diagnostic mentioning %q, got %v", want, ui.WarnCalls)
		}
	}
}

// TestEnsureImageWarnsOnLegacyDockerfileForms verifies the warnings are emitted
// through the UI when a project Dockerfile using a legacy dind spelling is
// ensured for building.
func TestEnsureImageWarnsOnLegacyDockerfileForms(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: "stale"},
				},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})

	project := []byte("FROM agents-sandbox/runner-base-dind\n# agents-sandbox:dind\nRUN echo hi\n")
	ui := &termio.Mock{}
	if _, err := EnsureImageWithClient(context.Background(), a, project, "proj", BuildOptions{}, ui); err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}

	joined := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{"runner-base-dind", "# agents-sandbox:dind"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a deprecation warning mentioning %q, got %v", want, ui.WarnCalls)
		}
	}
}

// TestEnsureImageWarnsOnEveryEnsure pins the documented behavior that the
// project-Dockerfile deprecations warn on every ensure, including a cached
// reuse: a Dockerfile carrying both legacy forms yields two warnings per call.
func TestEnsureImageWarnsOnEveryEnsure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: "stale"},
				},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})

	project := []byte("FROM agents-sandbox/runner-base-dind\n# agents-sandbox:dind\nRUN echo hi\n")
	ui := &termio.Mock{}
	for range 2 {
		if _, err := EnsureImageWithClient(context.Background(), a, project, "proj", BuildOptions{}, ui); err != nil {
			t.Fatalf("EnsureImageWithClient: %v", err)
		}
	}

	count := 0
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, "runner-base-dind") || strings.Contains(warning, "# agents-sandbox:dind") {
			count++
		}
	}
	if count != 4 {
		t.Errorf(
			"expected 2 deprecation warnings per ensure across 2 ensures (4 total), got %d: %v",
			count,
			ui.WarnCalls,
		)
	}
}

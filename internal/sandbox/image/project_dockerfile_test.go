package image

import (
	"strconv"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

// TestParsedProjectDockerfileSignalsAgree pins the single-parse contract: the
// diagnostic enablement, the rendered enablement, and the image identity's
// docker field all derive from one parsedProjectDockerfile, so for the same
// input they cannot disagree.
func TestParsedProjectDockerfileSignalsAgree(t *testing.T) {
	a := agentOpencode(t)
	cases := []struct {
		name    string
		project []byte
		setting options.DockerSetting
	}{
		{"no content, no config", []byte("FROM debian:trixie-slim\nRUN echo hi\n"), dockerOff},
		{"marker enables", []byte("FROM debian:trixie-slim\n# agents-sandbox:docker\nRUN echo hi\n"), dockerOff},
		{"managed docker base enables", []byte("FROM agents-sandbox/runner-base-docker\n"), dockerOff},
		{
			"explicit off beats marker",
			[]byte("FROM debian:trixie-slim\n# agents-sandbox:docker\n"),
			options.DockerSetting{Enabled: false, Explicit: true},
		},
		{"explicit on without content", []byte("FROM debian:trixie-slim\n"), dockerOn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := parseProjectDockerfile(tc.project)
			diagnostics := project.diagnostics(tc.setting)

			plan, err := renderBuildPlan(a, project, tc.setting, testAgentVersion, testAgentVersion)
			if err != nil {
				t.Fatalf("renderBuildPlan: %v", err)
			}
			identityDocker, err := strconv.ParseBool(plan.identity.Docker)
			if err != nil {
				t.Fatalf("identity Docker = %q: %v", plan.identity.Docker, err)
			}

			if identityDocker != diagnostics.Enabled {
				t.Errorf("identity docker = %v, diagnostics Enabled = %v", identityDocker, diagnostics.Enabled)
			}
			if got := project.effectiveDocker(tc.setting); got != diagnostics.Enabled {
				t.Errorf("render enablement = %v, diagnostics Enabled = %v", got, diagnostics.Enabled)
			}
			rendered := string(plan.rendered)
			if hasStage := strings.Contains(rendered, " AS "+dockerStageAlias); hasStage != identityDocker {
				t.Errorf("rendered docker stage present = %v, identity docker = %v", hasStage, identityDocker)
			}
		})
	}
}

// splitFinalStageForTest exposes the parsed final-stage split for the split
// tests. Production renders through parsedProjectDockerfile.render.
func splitFinalStageForTest(data []byte) (string, string, string) {
	p := parseProjectDockerfile(data)
	return p.earlier, p.finalBase, p.body
}

// TestRenderBuildPlanIdentityHashesProjectDockerfile pins that the parsed value
// carries the project bytes into the image identity: the identity's Dockerfile
// hash must be the hash of the project Dockerfile, not the absent-input sentinel.
func TestRenderBuildPlanIdentityHashesProjectDockerfile(t *testing.T) {
	project := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	plan, err := renderBuildPlan(
		agentOpencode(t), parseProjectDockerfile(project), dockerOff, testAgentVersion, testAgentVersion,
	)
	if err != nil {
		t.Fatalf("renderBuildPlan: %v", err)
	}
	if want := hashOrNone(project); plan.identity.ProjectDockerfileHash != want {
		t.Errorf("identity ProjectDockerfileHash = %q, want %q", plan.identity.ProjectDockerfileHash, want)
	}
}

// TestReservedAliasErrorSortsCollisions pins that the reported alias list is
// sorted rather than left in Go's randomized map-iteration order: the same
// sorted message must come out of every call.
func TestReservedAliasErrorSortsCollisions(t *testing.T) {
	p := parsedProjectDockerfile{stageBase: map[string]string{
		"agents-sandbox-runner": "image",
		"agents-sandbox-base":   "image",
		"agents-sandbox-agent":  "image",
	}}
	want := "project Dockerfile declares reserved stage alias(es): " +
		"agents-sandbox-agent, agents-sandbox-base, agents-sandbox-runner"
	for range 200 {
		if got := p.reservedAliasError(); got == nil || got.Error() != want {
			t.Fatalf("reservedAliasError() = %v, want %q", got, want)
		}
	}
}

// TestParseProjectDockerfileNoFromMarkerIsNotABodyMarker pins that a marker with
// no final FROM is not a body marker: without a final stage there is no body to
// inject into, so the content signal must stay off.
func TestParseProjectDockerfileNoFromMarkerIsNotABodyMarker(t *testing.T) {
	project := []byte("RUN echo hi\n# agents-sandbox:docker\n")
	p := parseProjectDockerfile(project)
	if p.marker != "" {
		t.Errorf("marker = %q, want none (a no-FROM file has no final-stage body)", p.marker)
	}
	if p.asksDocker() {
		t.Error("asksDocker() = true, want false: a no-FROM marker is not a body marker")
	}
	if p.effectiveDocker(dockerOff) {
		t.Error("effectiveDocker(off) = true, want false: a no-FROM marker must not enable docker")
	}
}

// injectDockerBlockForTest is the body-level convenience the injection tests
// use: it lexes the body with the same shared walk parseProjectDockerfile uses
// and finds the first top-level marker, then injects.
func injectDockerBlockForTest(body string, docker bool) (string, bool) {
	lines := dockerfileLines([]byte(body))
	_, topLevelLines := dockerfileLineIndices(lines)
	return injectDockerBlock(lines, firstMarkerIndex(lines, topLevelLines, 0), docker)
}

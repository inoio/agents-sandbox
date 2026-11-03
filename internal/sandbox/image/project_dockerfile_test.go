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

// injectDockerBlockForTest is the body-level convenience the injection tests
// use: it splits the body and finds its first marker itself, the same way
// parseProjectDockerfile does before calling injectDockerBlock.
func injectDockerBlockForTest(body string, docker bool) (string, bool) {
	lines := dockerfileLines([]byte(body))
	return injectDockerBlock(lines, firstMarkerIndex(lines), docker)
}

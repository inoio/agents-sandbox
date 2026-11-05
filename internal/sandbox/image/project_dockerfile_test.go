package image

import (
	"strconv"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

// TestParseProjectDockerfileSplitsTheFinalStage pins how a project Dockerfile
// splits into the stages before the final one, the final stage's FROM, and its
// body. No file, or a managed base, yields the embedded tools block as the base.
// A file without a FROM instruction is all base. FROM matches in any case, but
// not as a prefix of another word.
func TestParseProjectDockerfileSplitsTheFinalStage(t *testing.T) {
	tools := string(embeddedBaseToolsBlock)
	for _, tc := range []struct {
		name                     string
		project                  string
		earlier, finalBase, body string
	}{
		{name: "no project Dockerfile", finalBase: tools},
		{name: "no FROM", project: "RUN echo hi\n", finalBase: "RUN echo hi\n"},
		{
			name:      "FROMAGE is no FROM",
			project:   "FROMAGE debian:trixie-slim\nRUN echo hi\n",
			finalBase: "FROMAGE debian:trixie-slim\nRUN echo hi\n",
		},
		{
			name:      "managed base",
			project:   "FROM agents-sandbox/runner-base:latest\nRUN echo managed\n",
			finalBase: tools,
			body:      "RUN echo managed\n",
		},
		{
			name:      "custom base",
			project:   "FROM ubuntu:24.04\nRUN echo custom\n",
			finalBase: "FROM ubuntu:24.04\n",
			body:      "RUN echo custom\n",
		},
		{
			name:      "lowercase FROM",
			project:   "from debian:trixie-slim\nRUN echo hi\n",
			finalBase: "from debian:trixie-slim\n",
			body:      "RUN echo hi\n",
		},
		{
			name:      "mixed-case FROM",
			project:   "From debian:trixie-slim\nRUN echo hi\n",
			finalBase: "From debian:trixie-slim\n",
			body:      "RUN echo hi\n",
		},
		{
			name:      "multi-stage",
			project:   "FROM golang:1.24 AS build\nRUN go build\nFROM debian:trixie-slim AS final\nCOPY --from=build /app /app\n",
			earlier:   "FROM golang:1.24 AS build\nRUN go build\n",
			finalBase: "FROM debian:trixie-slim AS final\n",
			body:      "COPY --from=build /app /app\n",
		},
		{
			name:      "lowercase multi-stage",
			project:   "from golang:1.24 as build\nRUN go build\nfrom debian:trixie-slim as final\nCOPY --from=build /app /app\n",
			earlier:   "from golang:1.24 as build\nRUN go build\n",
			finalBase: "from debian:trixie-slim as final\n",
			body:      "COPY --from=build /app /app\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var project []byte
			if tc.project != "" {
				project = []byte(tc.project)
			}
			p := parseProjectDockerfile(project)
			if p.earlier != tc.earlier {
				t.Errorf("earlier = %q, want %q", p.earlier, tc.earlier)
			}
			if p.finalBase != tc.finalBase {
				t.Errorf("finalBase = %q, want %q", p.finalBase, tc.finalBase)
			}
			if p.body != tc.body {
				t.Errorf("body = %q, want %q", p.body, tc.body)
			}
		})
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

func TestReferencesImageIgnoresFromWithoutImage(t *testing.T) {
	if parseProjectDockerfile([]byte("FROM --platform=linux/amd64\n")).referencesImage("debian") {
		t.Error("expected referencesImage=false for a FROM line carrying only flags")
	}
}

func TestReferencesImageFalseForUnparseableRef(t *testing.T) {
	if parseProjectDockerfile([]byte("FROM INVALIDREF\n")).referencesImage("anything") {
		t.Error("expected referencesImage=false when the FROM reference cannot be parsed")
	}
}

func TestReferencesImageTerminatesOnCyclicStageAlias(t *testing.T) {
	// The project Dockerfile controls its own stage aliases, so it can name a
	// stage after its own base token or make two stages alias each other.
	// Resolving the final stage to its underlying image must terminate rather
	// than follow the cycle forever.
	for _, dockerfile := range []string{
		"FROM 0 AS 0\n",
		"FROM base AS builder\nFROM builder AS base\n",
	} {
		if parseProjectDockerfile([]byte(dockerfile)).referencesImage("agents-sandbox/runner-base") {
			t.Errorf("referencesImage(%q) = true, want false", dockerfile)
		}
	}
}

func TestReferencesImageDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest\nRUN echo hi\n")
	if !parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true for Dockerfile with base image")
	}
}

func TestReferencesImageReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false for non-base Dockerfile")
	}
}

func TestReferencesImageIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM agents-sandbox/runner-base:latest\nFROM debian:trixie-slim\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false for commented FROM")
	}
}

func TestReferencesImageMatchesImageIdentifierRegardlessOfTag(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:custom\nRUN echo hi\n")
	if !parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true for base image with a non-default tag")
	}
}

func TestReferencesImageMatchesMainImageWithStageAlias(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest AS builder\nRUN echo hi\n")
	if !parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true for base image declared as a stage")
	}
}

func TestReferencesImageIgnoresStageReference(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim AS base\nFROM base AS builder\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false when base image is used as a stage reference")
	}
}

func TestReferencesImageIgnoresBuildFlag(t *testing.T) {
	dockerfile := []byte("FROM --platform=linux/amd64 agents-sandbox/runner-base:latest\nRUN echo hi\n")
	if !parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true for base image after build flag")
	}
}

func TestReferencesImageFalseWhenBaseNotLastStage(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest AS base\nFROM debian:trixie-slim AS final\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false when base image is not the last stage")
	}
}

func TestReferencesImageTrueWhenBaseIsLastStageViaAlias(t *testing.T) {
	dockerfile := []byte(
		"FROM debian:trixie-slim AS base\nFROM agents-sandbox/runner-base:latest AS builder\nFROM builder\n",
	)
	if !parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true when base image backs the final stage through an alias")
	}
}

func TestReferencesImageIgnoresIntermediateStage(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest AS base\nFROM debian:trixie-slim AS final\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false for base image used only as an intermediate stage")
	}
}

func TestReferencesImageTrueWhenLastStageReusesAliasOfBase(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest AS base\nFROM base\n")
	if !parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true when the last stage reuses an alias of the base image")
	}
}

func TestReferencesImageDetectsDindImage(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	if !parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base-dind") {
		t.Error("expected referencesImage=true for Dockerfile with dind FROM")
	}
}

func TestReferencesImageReturnsFalseForPlainBase(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest\nRUN echo hi\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base-dind") {
		t.Error("expected referencesImage=false for plain base Dockerfile")
	}
}

func TestReferencesImageReturnsFalseForDindOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base-dind") {
		t.Error("expected referencesImage=false for non-base Dockerfile")
	}
}

func TestReferencesImageIgnoresDindComment(t *testing.T) {
	dockerfile := []byte("# FROM agents-sandbox/runner-base-dind:latest\nFROM debian:trixie-slim\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base-dind") {
		t.Error("expected referencesImage=false for commented FROM")
	}
}

func TestReferencesImageDindDoesNotMatchBase(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	if parseProjectDockerfile(dockerfile).referencesImage("agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false for dind Dockerfile with base image identifier (no false positive)")
	}
}

func TestEffectiveDindImpliedByManagedDindBase(t *testing.T) {
	project := []byte("FROM agents-sandbox/runner-base-dind:latest\n")
	if !parseProjectDockerfile(project).effectiveDocker(dockerOff) {
		t.Error("expected docker to be implied by the managed dind base")
	}
	if parseProjectDockerfile(nil).effectiveDocker(dockerOff) {
		t.Error("expected docker to be off without a flag or managed docker base")
	}
	if !parseProjectDockerfile(nil).effectiveDocker(dockerOn) {
		t.Error("expected docker to be on when the flag is set")
	}
}

func TestEffectiveDockerContentSignals(t *testing.T) {
	finalMarker := []byte("FROM debian:trixie-slim\n# agents-sandbox:docker\nRUN echo hi\n")
	customBase := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	cases := []struct {
		name    string
		project []byte
		setting options.DockerSetting
		want    bool
	}{
		{"marker enables with no config", finalMarker, dockerOff, true},
		{
			"legacy marker enables with no config",
			[]byte("FROM debian:trixie-slim\n# agents-sandbox:dind\nRUN echo hi\n"),
			dockerOff,
			true,
		},
		{"managed docker base enables", []byte("FROM agents-sandbox/runner-base-docker\n"), dockerOff, true},
		{"managed dind base enables", []byte("FROM agents-sandbox/runner-base-dind\n"), dockerOff, true},
		{"no signal stays off", customBase, dockerOff, false},
		{"explicit off beats marker", finalMarker, options.DockerSetting{Enabled: false, Explicit: true}, false},
		{
			"explicit off beats managed base",
			[]byte("FROM agents-sandbox/runner-base-docker\n"),
			options.DockerSetting{Enabled: false, Explicit: true},
			false,
		},
		{"explicit on without content", customBase, options.DockerSetting{Enabled: true, Explicit: true}, true},
		{
			"earlier-stage marker does not enable",
			[]byte("FROM debian:trixie-slim AS x\n# agents-sandbox:docker\nFROM debian:trixie-slim\nRUN echo hi\n"),
			dockerOff,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseProjectDockerfile(tc.project).effectiveDocker(tc.setting); got != tc.want {
				t.Errorf("effectiveDocker = %v, want %v", got, tc.want)
			}
		})
	}
}

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

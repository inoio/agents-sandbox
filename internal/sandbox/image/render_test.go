package image

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

var runnerStageFromLine = regexp.MustCompile(`(?m)^FROM \S+ AS ` + regexp.QuoteMeta(runnerStageAlias) + `$`)

func mustRender(t *testing.T, a agent.Agent, project []byte, docker bool) string {
	t.Helper()
	return string(renderBytes(t, a, project, docker))
}

// finalStage returns the text after the last "FROM ... AS agents-sandbox-runner"
// line, so a test can assert on the final image stage only. Docker drops ENV,
// LABEL, and other config directives set in non-final stages, so regressions
// that move them out of the final stage must not be visible here.
func finalStage(t *testing.T, rendered string) string {
	t.Helper()
	matches := runnerStageFromLine.FindAllStringIndex(rendered, -1)
	if len(matches) == 0 {
		t.Fatalf("rendered Dockerfile missing runner stage FROM line; got:\n%s", rendered)
	}
	return rendered[matches[len(matches)-1][1]:]
}

func TestRenderDockerfileManagedComposition(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	s := mustRender(t, a, nil, false)
	for _, want := range []string{
		"FROM debian:trixie-slim AS agents-sandbox-base",
		"FROM agents-sandbox-base AS agents-sandbox-node",
		"FROM agents-sandbox-node AS agents-sandbox-agent",
		"FROM agents-sandbox-base AS agents-sandbox-runner",
		"ARG OPENCODE_VERSION",
		"ARG DOCKERFILE_ID",
		"LABEL org.agents-sandbox.dockerfile-id=$DOCKERFILE_ID",
		"nodejs.org/dist/v26.8.1",
		"> /etc/agents-sandbox/agent-source",
		"COPY --from=agents-sandbox-agent /opt/agents-sandbox /opt/agents-sandbox",
		`ENV PATH="${PATH}:/opt/agents-sandbox/bin"`,
		"groupadd -f -g \"$USER_GID\" dev",
		"LABEL org.agents-sandbox.managed=true",
		"WORKDIR /workspace",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
	if strings.Contains(s, "agents-sandbox-docker") {
		t.Error("docker stage must be absent when docker is disabled")
	}
}

// TestRenderDockerfileAgentConfigInFinalStage guards the multistage regression
// where the agent ENV and label were emitted into a non-final stage: Docker
// drops config directives not reachable from the final FROM, so agent env (read
// back from image inspect) and provenance must live in the runner stage.
func TestRenderDockerfileAgentConfigInFinalStage(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	s := mustRender(t, a, nil, false)
	final := finalStage(t, s)
	spec := a.ImageSpec()
	for _, want := range []string{
		"LABEL org.agents-sandbox.agent=" + a.Name(),
	} {
		if !strings.Contains(final, want) {
			t.Errorf("final runner stage missing %q; got:\n%s", want, final)
		}
	}
	if len(spec.AgentEnv) == 0 {
		t.Fatal("test agent has no AgentEnv to assert")
	}
	for k, v := range spec.AgentEnv {
		want := "ENV " + k + "=" + v
		if !strings.Contains(final, want) {
			t.Errorf("final runner stage missing %q; got:\n%s", want, final)
		}
	}
	if !strings.Contains(final, "OPENCODE_DISABLE_AUTOUPDATE=true") {
		t.Errorf("final runner stage missing OPENCODE_DISABLE_AUTOUPDATE=true; got:\n%s", final)
	}
}

// TestRenderDockerfileFinalStageUsesAppendingPath guards requirement 2: the
// runner appends /opt/agents-sandbox/bin so a base-provided binary keeps
// precedence. The node stage may still prepend internally.
func TestRenderDockerfileFinalStageUsesAppendingPath(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	s := mustRender(t, a, nil, false)
	final := finalStage(t, s)
	if !strings.Contains(final, `ENV PATH="${PATH}:/opt/agents-sandbox/bin"`) {
		t.Errorf("final runner stage must append toolBin to PATH; got:\n%s", final)
	}
	if strings.Contains(final, `ENV PATH="/opt/agents-sandbox/bin:${PATH}"`) {
		t.Errorf("final runner stage must not prepend toolBin to PATH; got:\n%s", final)
	}
}

func TestRenderDockerfileStageOrder(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	s := mustRender(t, a, nil, true)
	order := []string{
		"FROM debian:trixie-slim AS agents-sandbox-base",
		"FROM agents-sandbox-base AS agents-sandbox-node",
		"FROM agents-sandbox-node AS agents-sandbox-agent",
		"FROM agents-sandbox-base AS agents-sandbox-docker",
		"FROM agents-sandbox-base AS agents-sandbox-runner",
	}
	prev := -1
	for _, stage := range order {
		idx := strings.Index(s, stage)
		if idx < 0 {
			t.Fatalf("rendered Dockerfile missing stage %q", stage)
		}
		if idx < prev {
			t.Errorf("stage %q out of order", stage)
		}
		prev = idx
	}
}

func TestRenderDockerfileDockerStage(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	s := mustRender(t, a, nil, true)
	for _, want := range []string{
		"FROM agents-sandbox-base AS agents-sandbox-docker",
		"ARG DOCKER_VERSION=29.7.2",
		"download.docker.com/linux/static/stable/$(uname -m)/docker-${DOCKER_VERSION}.tgz",
		"COPY --from=agents-sandbox-docker /opt/agents-sandbox /opt/agents-sandbox",
		"> /etc/agents-sandbox/docker-source",
		"groupadd -f docker",
		"usermod -aG docker dev",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
	if strings.Contains(s, "daemon.json") {
		t.Error("the vfs storage driver must not be written to daemon.json at build time")
	}
}

// TestRenderDockerfileDockerAdoptionAddsDevToGroup guards C1: the docker
// adoption block in the final stage must add dev to the docker group, or the
// root:docker 0660 socket leaves dev unable to run `docker info`.
func TestRenderDockerfileDockerAdoptionAddsDevToGroup(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	s := mustRender(t, a, nil, true)
	final := finalStage(t, s)
	if !strings.Contains(final, "usermod -aG docker dev") {
		t.Errorf("final runner stage must add dev to the docker group; got:\n%s", final)
	}
}

// TestRenderDockerfilePostBodyBlocksResetUserRoot guards I2: a user body ending
// in a non-root USER must not leak into the tool-owned adoption RUN blocks.
func TestRenderDockerfilePostBodyBlocksResetUserRoot(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM ubuntu:24.04\nRUN echo custom\nUSER 1000\n")

	// The agent adoption block is isolated by slicing from its preceding COPY
	// (which resets the copy context, not the user) up to the agent-source RUN.
	// The docker merge block sits in between when docker is enabled, so slicing
	// from the body would pick up docker's own USER root and make the agent
	// assertion vacuous.
	t.Run("agent block with docker enabled", func(t *testing.T) {
		s := mustRender(t, a, project, true)
		agentCopyIdx := strings.Index(s, "COPY --from="+agentStageAlias)
		agentIdx := strings.Index(s, "> /etc/agents-sandbox/agent-source")
		if agentCopyIdx < 0 || agentIdx < 0 || agentCopyIdx > agentIdx {
			t.Fatalf("rendered Dockerfile missing expected agent markers; got:\n%s", s)
		}
		agentBlock := s[agentCopyIdx:agentIdx]
		if !strings.Contains(agentBlock, "USER root") {
			t.Errorf("agent adoption block must reset to USER root after a non-root body; got:\n%s", agentBlock)
		}
	})

	t.Run("agent block with docker disabled", func(t *testing.T) {
		s := mustRender(t, a, project, false)
		bodyIdx := strings.Index(s, "RUN echo custom")
		agentIdx := strings.Index(s, "> /etc/agents-sandbox/agent-source")
		if bodyIdx < 0 || agentIdx < 0 || bodyIdx > agentIdx {
			t.Fatalf("rendered Dockerfile missing expected markers; got:\n%s", s)
		}
		agentBlock := s[bodyIdx:agentIdx]
		if strings.Contains(agentBlock, dockerAdoptionBlock()) {
			t.Fatalf("agent block slice unexpectedly contains the docker block; got:\n%s", agentBlock)
		}
		if !strings.Contains(agentBlock, "USER root") {
			t.Errorf("agent adoption block must reset to USER root after a non-root body; got:\n%s", agentBlock)
		}
	})

	// The docker adoption block follows the docker merge COPY, which is the
	// nearest preceding reset; slice from the body through the docker-source RUN.
	s := mustRender(t, a, project, true)
	bodyIdx := strings.Index(s, "RUN echo custom")
	dockerIdx := strings.Index(s, "> /etc/agents-sandbox/docker-source")
	if bodyIdx < 0 || dockerIdx < 0 || bodyIdx > dockerIdx {
		t.Fatalf("rendered Dockerfile missing expected docker markers; got:\n%s", s)
	}
	dockerBlock := s[bodyIdx:dockerIdx]
	if !strings.Contains(dockerBlock, "USER root") {
		t.Errorf("docker adoption block must reset to USER root after a non-root body; got:\n%s", dockerBlock)
	}
}

func TestRenderDockerfileOpencode2(t *testing.T) {
	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("opencode2 agent not registered")
	}
	s := mustRender(t, a, nil, false)
	for _, want := range []string{
		"ARG OPENCODE2_VERSION",
		"LABEL org.agents-sandbox.agent=opencode2",
		"npm install -g --prefix /opt/agents-sandbox @opencode-ai/cli@$OPENCODE2_VERSION",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered opencode2 Dockerfile missing %q", want)
		}
	}
}

func TestRenderDockerfileOpencodeInstallTargetsOpt(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	s := mustRender(t, a, nil, false)
	if !strings.Contains(s, "cp /root/.opencode/bin/opencode /opt/agents-sandbox/bin") {
		t.Errorf("opencode installer must land the binary under /opt/agents-sandbox/bin; got:\n%s", s)
	}
}

func TestRenderDockerfileCustomBase(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM ubuntu:24.04\nRUN apt-get update && apt-get install -y curl bash\n")
	s := mustRender(t, a, project, false)
	if !strings.Contains(s, "FROM ubuntu:24.04 AS agents-sandbox-base") {
		t.Errorf("custom base must be given the reserved base alias; got:\n%s", s)
	}
	if strings.Contains(s, "iptables") {
		t.Error("custom base must not get the apt base tools block")
	}
	bodyIdx := strings.Index(s, "apt-get install -y curl bash")
	runnerIdx := strings.Index(s, "FROM ubuntu:24.04 AS agents-sandbox-base")
	devIdx := strings.Index(s, `groupadd -f -g "$USER_GID" dev`)
	copyIdx := strings.Index(s, "COPY --from=agents-sandbox-agent")
	if bodyIdx < 0 || devIdx < 0 || copyIdx < 0 {
		t.Fatalf("rendered Dockerfile missing expected markers; got:\n%s", s)
	}
	if runnerIdx > devIdx {
		t.Error("runner stage FROM must precede the dev user block")
	}
	if devIdx > bodyIdx {
		t.Error("dev user must be created before the user-provided body")
	}
	if bodyIdx > copyIdx {
		t.Error("the tool COPY must run after the user body")
	}
}

func TestRenderDockerfileCustomBaseReusesAlias(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte(
		"FROM golang:1.24 AS build\nRUN go build -o /app .\nFROM debian:trixie-slim AS final\nCOPY --from=build /app /app\n",
	)
	s := mustRender(t, a, project, false)
	for _, want := range []string{
		"FROM golang:1.24 AS build",
		"FROM debian:trixie-slim AS final",
		"FROM final AS agents-sandbox-node",
		"FROM final AS agents-sandbox-runner",
		"COPY --from=build /app /app",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q; got:\n%s", want, s)
		}
	}
	if strings.Contains(s, "agents-sandbox-base") {
		t.Error("a user-declared final alias must be reused as the base alias")
	}
}

func TestRenderDockerfileManagedBaseFrom(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM agents-sandbox/runner-base:latest\nRUN apt-get install -y tree\n")
	s := mustRender(t, a, project, false)
	if !strings.Contains(s, "FROM debian:trixie-slim AS agents-sandbox-base") {
		t.Error("managed FROM must be replaced with the embedded base tools block")
	}
	if strings.Contains(s, "agents-sandbox/runner-base") {
		t.Error("managed base reference must be replaced, not kept")
	}
	if !strings.Contains(s, "RUN apt-get install -y tree") {
		t.Error("user body must be preserved after the managed FROM")
	}
}

func TestRenderDockerfileDindFromImpliesDocker(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	s := mustRender(t, a, project, false)
	if !strings.Contains(s, "agents-sandbox-docker") {
		t.Error("a runner-base-dind FROM must imply docker")
	}
}

func TestRenderDockerfileDockerFromImpliesDocker(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM agents-sandbox/runner-base-docker:latest\nRUN echo hi\n")
	s := mustRender(t, a, project, false)
	if !strings.Contains(s, "agents-sandbox-docker") {
		t.Error("a runner-base-docker FROM must imply docker")
	}
}

func TestRenderDockerfileDockerMarkerInjection(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte(
		"FROM ubuntu:24.04\n" +
			"RUN apt-get install -y iptables\n" +
			"# agents-sandbox:docker\n" +
			"RUN echo post-docker\n",
	)
	s := mustRender(t, a, project, true)
	if strings.Contains(s, "# agents-sandbox:docker") {
		t.Error("the docker marker must be replaced in the rendered Dockerfile")
	}
	prereqIdx := strings.Index(s, "RUN apt-get install -y iptables")
	copyIdx := strings.Index(s, "COPY --from=agents-sandbox-docker")
	tailIdx := strings.Index(s, "RUN echo post-docker")
	if prereqIdx < 0 || copyIdx < 0 || tailIdx < 0 {
		t.Fatalf("rendered Dockerfile missing expected markers; got:\n%s", s)
	}
	if prereqIdx >= copyIdx || copyIdx >= tailIdx {
		t.Error("the docker copy must be injected at the marker, after prerequisites and before the tail")
	}
}

func TestRenderDockerfileLegacyDindMarkerInjection(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte(
		"FROM ubuntu:24.04\n" +
			"RUN apt-get install -y iptables\n" +
			"# agents-sandbox:dind\n" +
			"RUN echo post-dind\n",
	)
	s := mustRender(t, a, project, true)
	if strings.Contains(s, "# agents-sandbox:dind") {
		t.Error("the legacy dind marker must be replaced in the rendered Dockerfile")
	}
	if !strings.Contains(s, "COPY --from=agents-sandbox-docker") {
		t.Error("the legacy dind marker must still inject the docker copy")
	}
}

func TestRenderDockerfileDockerMarkerAbsent(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM ubuntu:24.04\nRUN echo custom\n")
	s := mustRender(t, a, project, true)
	bodyIdx := strings.Index(s, "RUN echo custom")
	copyIdx := strings.Index(s, "COPY --from=agents-sandbox-docker")
	if bodyIdx < 0 || copyIdx < 0 || copyIdx < bodyIdx {
		t.Error("without a marker the docker copy must be appended after the user body")
	}
}

func TestRenderDockerfileReservedAliasCollision(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	for _, alias := range []string{
		baseStageAlias, nodeStageAlias, agentStageAlias, dockerStageAlias, runnerStageAlias,
	} {
		project := []byte("FROM ubuntu:24.04 AS " + alias + "\nRUN echo hi\n")
		if _, err := RenderDockerfile(a, project, false); err == nil {
			t.Errorf("declaring reserved alias %q must be a hard error", alias)
		}
	}
}

// TestRenderDockerfileReservedAliasCaseInsensitive guards the minor finding:
// Docker lower-cases stage names, so an upper-case reserved alias collides too.
func TestRenderDockerfileReservedAliasCaseInsensitive(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM ubuntu:24.04 AS AGENTS-SANDBOX-BASE\nRUN echo hi\n")
	_, err := RenderDockerfile(a, project, false)
	if err == nil {
		t.Fatal("declaring reserved alias AGENTS-SANDBOX-BASE must be a hard error")
	}
	if !strings.Contains(err.Error(), "AGENTS-SANDBOX-BASE") {
		t.Errorf("error must report the alias spelling from the Dockerfile; got %q", err)
	}
}

func TestRenderDockerfileDeterministic(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM ubuntu:24.04\n# agents-sandbox:docker\nRUN echo hi\n")
	first := mustRender(t, a, project, true)
	second := mustRender(t, a, project, true)
	if first != second {
		t.Error("rendered Dockerfile must be deterministic")
	}
}

func TestSplitFinalStageNoProjectDockerfile(t *testing.T) {
	earlier, base, body := splitFinalStage(nil)
	if earlier != "" {
		t.Errorf("earlier = %q, want empty", earlier)
	}
	if base != string(embeddedBaseToolsBlock) {
		t.Errorf("base = %q, want the embedded tools block", base)
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

func TestSplitFinalStageManagedBase(t *testing.T) {
	project := []byte("FROM agents-sandbox/runner-base:latest\nRUN echo managed\n")
	earlier, base, body := splitFinalStage(project)
	if earlier != "" {
		t.Errorf("earlier = %q, want empty", earlier)
	}
	if base != string(embeddedBaseToolsBlock) {
		t.Errorf("base = %q, want the embedded tools block", base)
	}
	if body != "RUN echo managed\n" {
		t.Errorf("body = %q, want the user body", body)
	}
}

func TestSplitFinalStageCustomBase(t *testing.T) {
	project := []byte("FROM ubuntu:24.04\nRUN echo custom\n")
	earlier, base, body := splitFinalStage(project)
	if earlier != "" {
		t.Errorf("earlier = %q, want empty", earlier)
	}
	if base != "FROM ubuntu:24.04\n" {
		t.Errorf("base = %q, want the user's final FROM", base)
	}
	if body != "RUN echo custom\n" {
		t.Errorf("body = %q, want the user body", body)
	}
}

func TestSplitFinalStageMultiStage(t *testing.T) {
	project := []byte(
		"FROM golang:1.24 AS build\nRUN go build\n" +
			"FROM debian:trixie-slim AS final\nCOPY --from=build /app /app\n",
	)
	earlier, base, body := splitFinalStage(project)
	wantEarlier := "FROM golang:1.24 AS build\nRUN go build\n"
	if earlier != wantEarlier {
		t.Errorf("earlier = %q, want %q", earlier, wantEarlier)
	}
	if base != "FROM debian:trixie-slim AS final\n" {
		t.Errorf("base = %q, want the final FROM", base)
	}
	if body != "COPY --from=build /app /app\n" {
		t.Errorf("body = %q, want the final-stage body", body)
	}
}

func TestCustomBaseStageAddsAlias(t *testing.T) {
	stage, alias := customBaseStage("FROM ubuntu:24.04\n")
	if alias != baseStageAlias {
		t.Errorf("alias = %q, want %q", alias, baseStageAlias)
	}
	if !strings.Contains(stage, "AS "+baseStageAlias) {
		t.Errorf("stage = %q, want reserved alias", stage)
	}
}

func TestCustomBaseStageReusesDeclaredAlias(t *testing.T) {
	stage, alias := customBaseStage("FROM ubuntu:24.04 AS base\n")
	if alias != "base" {
		t.Errorf("alias = %q, want %q", alias, "base")
	}
	if stage != "FROM ubuntu:24.04 AS base\n" {
		t.Errorf("stage = %q, want the line unchanged", stage)
	}
}

func TestAgentImageConfigBlock(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	block := agentImageConfigBlock(a)
	for _, want := range []string{
		"ENV OPENCODE_DISABLE_AUTOUPDATE=true",
		"ENV OPENCODE_EXPERIMENTAL_WORKSPACES=true",
		"LABEL org.agents-sandbox.agent=opencode",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("agentImageConfigBlock missing %q; got:\n%s", want, block)
		}
	}
	if strings.Index(block, "OPENCODE_DISABLE_AUTOUPDATE") > strings.Index(block, "OPENCODE_EXPERIMENTAL_WORKSPACES") {
		t.Errorf("ENV keys must be sorted; got:\n%s", block)
	}
}

func TestInjectDockerBlockAtMarker(t *testing.T) {
	body := "RUN install-prereqs\n# agents-sandbox:docker\nRUN configure\n"
	got, injected := injectDockerBlock(body, true)
	if !injected {
		t.Fatal("expected injection at the marker")
	}
	want := "RUN install-prereqs\n" + dockerMergeBlock() + "RUN configure\n"
	if got != want {
		t.Errorf("injectDockerBlock = %q, want %q", got, want)
	}
}

func TestInjectDockerBlockAppendsWithoutMarker(t *testing.T) {
	got, injected := injectDockerBlock("RUN install-prereqs\n", true)
	if injected {
		t.Error("expected append, not marker injection")
	}
	if got != "RUN install-prereqs\n" {
		t.Errorf("injectDockerBlock = %q, want the body unchanged", got)
	}
}

func TestInjectDockerBlockNoopWhenDisabled(t *testing.T) {
	got, injected := injectDockerBlock("RUN x\n", false)
	if injected || got != "RUN x\n" {
		t.Errorf("injectDockerBlock = %q (injected=%v), want unchanged", got, injected)
	}
}

func TestJoinBlocksSkipsEmpty(t *testing.T) {
	got := joinBlocks("", "FROM debian\n", "", "RUN x\n")
	want := "FROM debian\n\nRUN x\n"
	if got != want {
		t.Errorf("joinBlocks = %q, want %q", got, want)
	}
}

func TestBaseImageRefComposition(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if got := baseImageRef(renderBytes(t, a, nil, false)); got != "debian:trixie-slim" {
		t.Errorf("managed baseImageRef = %q, want debian:trixie-slim", got)
	}
	custom := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, custom, false)); got != "ubuntu:24.04" {
		t.Errorf("custom baseImageRef = %q, want ubuntu:24.04", got)
	}
	aliased := []byte("FROM fedora:latest AS base\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, aliased, false)); got != "fedora:latest" {
		t.Errorf("aliased baseImageRef = %q, want fedora:latest", got)
	}
}

// projectDockerfilePaths is a minimal configpaths.ConfigPaths pointing at a
// temp project dir so tests can exercise on-disk Dockerfile reading.
type projectDockerfilePaths struct {
	dir string
}

func (p *projectDockerfilePaths) ProjectDockerfile() string {
	return filepath.Join(p.dir, configpaths.DockerFileName)
}

func (p *projectDockerfilePaths) UserConfigDir() string                 { return "" }
func (p *projectDockerfilePaths) UserCacheDir() string                  { return "" }
func (p *projectDockerfilePaths) UserStateDir() string                  { return "" }
func (p *projectDockerfilePaths) UserAgentConfigDir(agent.Agent) string { return "" }
func (p *projectDockerfilePaths) UserEnvFile() string                   { return "" }
func (p *projectDockerfilePaths) UserEnvSecretFile() string             { return "" }
func (p *projectDockerfilePaths) UserEnvSecretYAMLFile() string         { return "" }
func (p *projectDockerfilePaths) ProjectConfigDir() string              { return "" }
func (p *projectDockerfilePaths) ProjectAgentConfigDir(agent.Agent) string {
	return ""
}
func (p *projectDockerfilePaths) ProjectEnvFile() string           { return "" }
func (p *projectDockerfilePaths) ProjectEnvSecretFile() string     { return "" }
func (p *projectDockerfilePaths) ProjectEnvSecretYAMLFile() string { return "" }

func TestRenderProjectDockerfileReadsProjectFile(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	dir := t.TempDir()
	projectFile := filepath.Join(dir, configpaths.DockerFileName)
	if err := os.WriteFile(projectFile, []byte("FROM ubuntu:24.04\nRUN echo custom\n"), 0o644); err != nil {
		t.Fatalf("write project dockerfile: %v", err)
	}

	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return &projectDockerfilePaths{dir: dir} }
	t.Cleanup(func() { configpaths.Get = orig })

	out, err := RenderProjectDockerfile(a, false)
	if err != nil {
		t.Fatalf("RenderProjectDockerfile: %v", err)
	}
	for _, want := range []string{"FROM ubuntu:24.04 AS agents-sandbox-base", "RUN echo custom"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("rendered project Dockerfile missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "agents-sandbox-docker") {
		t.Errorf("project Dockerfile without --docker must not contain the docker stage")
	}
}

func TestRenderProjectDockerfileNoProjectFile(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	dir := t.TempDir()

	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return &projectDockerfilePaths{dir: dir} }
	t.Cleanup(func() { configpaths.Get = orig })

	out, err := RenderProjectDockerfile(a, false)
	if err != nil {
		t.Fatalf("RenderProjectDockerfile: %v", err)
	}
	if !strings.Contains(string(out), "FROM debian:trixie-slim AS agents-sandbox-base") {
		t.Errorf("without a project Dockerfile the embedded debian base is used; got:\n%s", out)
	}
}

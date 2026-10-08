package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func mustRender(t *testing.T, a agent.Agent, project []byte, docker bool) string {
	t.Helper()
	return string(renderBytes(t, a, project, docker))
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
		"LABEL org.agents-sandbox.agent=opencode",
		"ARG DOCKERFILE_ID",
		"LABEL org.agents-sandbox.dockerfile-id=$DOCKERFILE_ID",
		"OPENCODE_DISABLE_AUTOUPDATE=true",
		"nodejs.org/dist/v26.8.1",
		"> /etc/agents-sandbox/agent-source",
		"COPY --from=agents-sandbox-agent /opt/agents-sandbox /opt/agents-sandbox",
		`ENV PATH="/opt/agents-sandbox/bin:${PATH}"`,
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
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
	if strings.Contains(s, "daemon.json") {
		t.Error("the vfs storage driver must not be written to daemon.json at build time")
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

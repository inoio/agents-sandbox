package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestRenderDockerfileDefaultBase(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	out := RenderDockerfile(a, nil, false)
	s := string(out)
	for _, want := range []string{
		"FROM debian:trixie-slim",
		"iptables",
		"ARG OPENCODE_VERSION",
		"LABEL org.agents-sandbox.agent=opencode",
		"ARG DOCKERFILE_ID",
		"LABEL org.agents-sandbox.dockerfile-id=$DOCKERFILE_ID",
		"OPENCODE_DISABLE_AUTOUPDATE=true",
		"node-v26.8.1-linux",
		"echo tool > /etc/agents-sandbox/agent-source",
		"echo user > /etc/agents-sandbox/agent-source",
		"groupadd -f -g \"$USER_GID\" dev",
		"useradd -m -u \"$USER_UID\" -g dev -s \"$p\" dev",
		"for sh in bash zsh sh; do",
		"LABEL org.agents-sandbox.managed=true",
		"USER dev",
		"WORKDIR /workspace",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"nodesource", "AGENT_INSTALL_BLOCK", "docker-ce", "runner-base",
		"org.agents-sandbox.opencode-version", "DOCKER_VERSION",
	} {
		if strings.Contains(s, unwanted) {
			t.Errorf("rendered Dockerfile must not contain %q", unwanted)
		}
	}
}

func TestRenderDockerfileDindEnabled(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	out := RenderDockerfile(a, nil, true)
	s := string(out)
	for _, want := range []string{
		"ARG DOCKER_VERSION=29.7.2",
		"download.docker.com/linux/static/stable/$(uname -m)/docker-${DOCKER_VERSION}.tgz",
		"echo tool > /etc/agents-sandbox/docker-source",
		"echo user > /etc/agents-sandbox/docker-source",
		`echo '{"storage-driver":"vfs"}' > /etc/docker/daemon.json`,
		"groupadd -f docker",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
	dindIdx := strings.Index(s, "DOCKER_VERSION")
	agentIdx := strings.Index(s, "LABEL org.agents-sandbox.agent")
	if dindIdx < 0 || agentIdx < 0 || dindIdx > agentIdx {
		t.Error("dind block must come before the agent block")
	}
	devIdx := strings.Index(s, `groupadd -f -g "$USER_GID" dev`)
	dockerIdx := strings.Index(s, "groupadd -f docker")
	if devIdx < 0 || dockerIdx < 0 || devIdx > dockerIdx {
		t.Error("dev user must be created before the docker group is added")
	}
}

// TestRenderDockerfileOpencode2 checks that the opencode2 beta agent renders
// its npm install block with the correct build ARG and agent label.
func TestRenderDockerfileOpencode2(t *testing.T) {
	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("opencode2 agent not registered")
	}
	out := string(RenderDockerfile(a, nil, false))
	for _, want := range []string{
		"ARG OPENCODE2_VERSION",
		"LABEL org.agents-sandbox.agent=opencode2",
		"OPENCODE_DISABLE_AUTOUPDATE=true",
		"npm install -g @opencode-ai/cli@$OPENCODE2_VERSION",
		"echo tool > /etc/agents-sandbox/agent-source",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered opencode2 Dockerfile missing %q", want)
		}
	}
}

func TestRenderDockerfileCustomBase(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM ubuntu:24.04\nRUN apt-get update && apt-get install -y curl bash\n")
	out := RenderDockerfile(a, project, false)
	s := string(out)
	if !strings.HasPrefix(s, "FROM ubuntu:24.04") {
		t.Errorf("custom base must keep its FROM, got %q", firstLine(s))
	}
	if strings.Contains(s, "iptables") {
		t.Error("custom base must not get the apt base tools block")
	}
	for _, want := range []string{"LABEL org.agents-sandbox.agent=opencode", "WORKDIR /workspace"} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
}

func TestRenderDockerfileManagedBaseFrom(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM agents-sandbox/runner-base:latest\nRUN apt-get install -y tree\n")
	out := RenderDockerfile(a, project, false)
	s := string(out)
	if !strings.Contains(s, "FROM debian:trixie-slim") {
		t.Error("managed FROM must be replaced with the embedded base tools block")
	}
	if strings.Contains(s, "agents-sandbox/runner-base") {
		t.Error("managed base reference must be replaced, not kept")
	}
	if !strings.Contains(s, "RUN apt-get install -y tree") {
		t.Error("user body must be preserved after the managed FROM")
	}
	devIdx := strings.Index(s, `groupadd -f -g "$USER_GID" dev`)
	bodyIdx := strings.Index(s, "apt-get install -y tree")
	if devIdx < 0 || bodyIdx < 0 || devIdx > bodyIdx {
		t.Error("dev user must be created before the user-provided Dockerfile body (first in final stage)")
	}
}

func TestRenderDockerfileDevUserInFinalStage(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte(
		"FROM golang:1.24 AS build\nRUN go build -o /app .\nFROM debian:trixie-slim AS final\nCOPY --from=build /app /app\n",
	)
	out := RenderDockerfile(a, project, true)
	s := string(out)
	lastFromIdx := strings.LastIndex(s, "FROM ")
	devIdx := strings.Index(s, `groupadd -f -g "$USER_GID" dev`)
	dockerIdx := strings.Index(s, "groupadd -f docker")
	if lastFromIdx < 0 || devIdx < 0 || dockerIdx < 0 {
		t.Fatal("rendered Dockerfile missing expected markers")
	}
	if devIdx < lastFromIdx {
		t.Error("dev user block must be created after the final stage FROM in a multi-stage build")
	}
	if devIdx > dockerIdx {
		t.Error("dev user must be created before the dind docker group")
	}
}

func TestRenderDockerfileManagedMultiStage(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte(
		"FROM golang:1.24 AS build\nRUN go build -o /app .\nFROM agents-sandbox/runner-base-dind:latest\nCOPY --from=build /app /app\n",
	)
	out := RenderDockerfile(a, project, false)
	s := string(out)
	buildFromIdx := strings.Index(s, "FROM golang:1.24 AS build")
	baseFromIdx := strings.Index(s, "FROM debian:trixie-slim")
	devIdx := strings.Index(s, `groupadd -f -g "$USER_GID" dev`)
	bodyIdx := strings.Index(s, "COPY --from=build /app /app")
	if buildFromIdx < 0 || baseFromIdx < 0 || devIdx < 0 || bodyIdx < 0 {
		t.Fatal("rendered Dockerfile missing expected markers")
	}
	if baseFromIdx < buildFromIdx {
		t.Error("embedded base tools block must replace the managed FROM in the final stage, after the build stage")
	}
	if devIdx < baseFromIdx {
		t.Error("dev user block must be the first instruction of the final stage")
	}
	if devIdx > bodyIdx {
		t.Error("dev user must be created before the user's final-stage body")
	}
}

func TestRenderDockerfileDevUserAfterBaseTools(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	out := RenderDockerfile(a, nil, false)
	s := string(out)
	devIdx := strings.Index(s, `groupadd -f -g "$USER_GID" dev`)
	toolsIdx := strings.Index(s, "iptables")
	if devIdx < 0 || toolsIdx < 0 || devIdx < toolsIdx {
		t.Error("dev user must be created after the base tools so a host UID/GID change keeps the tools layer cached")
	}
}

func TestRenderDockerfileDindFromImpliesDind(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	out := RenderDockerfile(a, project, false)
	if !strings.Contains(string(out), "DOCKER_VERSION") {
		t.Error("a runner-base-dind FROM must imply the dind block even without the flag")
	}
}

func TestRenderDockerfileDindMarker(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte(
		"FROM ubuntu:24.04\n" +
			"RUN apt-get install -y iptables\n" +
			"# agents-sandbox:dind\n" +
			"RUN echo post-dind\n",
	)
	out := string(RenderDockerfile(a, project, true))
	if strings.Contains(out, "# agents-sandbox:dind") {
		t.Error("the dind marker must be replaced in the rendered Dockerfile")
	}
	prereqIdx := strings.Index(out, "RUN apt-get install -y iptables")
	dindIdx := strings.Index(out, "DOCKER_VERSION")
	tailIdx := strings.Index(out, "RUN echo post-dind")
	if prereqIdx < 0 || dindIdx < 0 || tailIdx < 0 {
		t.Fatal("rendered Dockerfile missing expected markers")
	}
	if prereqIdx >= dindIdx || dindIdx >= tailIdx {
		t.Error("dind block must be injected at the marker, after prerequisites and before the tail")
	}
}

func TestRenderDockerfileDindMarkerAbsent(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM ubuntu:24.04\nRUN echo custom\n")
	out := string(RenderDockerfile(a, project, true))
	bodyIdx := strings.Index(out, "RUN echo custom")
	dindIdx := strings.Index(out, "DOCKER_VERSION")
	if bodyIdx < 0 || dindIdx < 0 || dindIdx < bodyIdx {
		t.Error("without a marker the dind block must be appended after the user body")
	}
}

func TestRenderDockerfileDockerfileIDLabelAfterAgentInstall(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	out := string(RenderDockerfile(a, nil, false))
	installIdx := strings.Index(out, "https://opencode.ai/install")
	labelIdx := strings.Index(out, "LABEL org.agents-sandbox.dockerfile-id")
	if installIdx < 0 || labelIdx < 0 || labelIdx < installIdx {
		t.Error(
			"dockerfile-id label must come after the agent install so an agent upgrade keeps the install layer cached",
		)
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

func TestInjectDindBlockAtMarker(t *testing.T) {
	body := "RUN install-prereqs\n# agents-sandbox:dind\nRUN configure\n"
	got := injectDindBlock(body, "DIND\n")
	want := "RUN install-prereqs\nDIND\nRUN configure\n"
	if got != want {
		t.Errorf("injectDindBlock = %q, want %q", got, want)
	}
}

func TestInjectDindBlockAppendsWithoutMarker(t *testing.T) {
	got := injectDindBlock("RUN install-prereqs\n", "DIND\n")
	want := "RUN install-prereqs\nDIND\n"
	if got != want {
		t.Errorf("injectDindBlock = %q, want %q", got, want)
	}
}

func TestInjectDindBlockAppendsToEmptyBody(t *testing.T) {
	if got := injectDindBlock("", "DIND\n"); got != "DIND\n" {
		t.Errorf("injectDindBlock = %q, want %q", got, "DIND\n")
	}
}

func TestJoinBlocksSkipsEmpty(t *testing.T) {
	got := joinBlocks("", "FROM debian\n", "", "RUN x\n")
	want := "FROM debian\n\nRUN x\n"
	if got != want {
		t.Errorf("joinBlocks = %q, want %q", got, want)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
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

	out := string(RenderProjectDockerfile(a, false))
	for _, want := range []string{"FROM ubuntu:24.04", "RUN echo custom"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered project Dockerfile missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DOCKER_VERSION") {
		t.Errorf("project Dockerfile without --dind must not contain the dind block")
	}
}

func TestRenderProjectDockerfileNoProjectFile(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	dir := t.TempDir()

	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return &projectDockerfilePaths{dir: dir} }
	t.Cleanup(func() { configpaths.Get = orig })

	out := string(RenderProjectDockerfile(a, false))
	if !strings.Contains(out, "FROM debian:trixie-slim") {
		t.Errorf("without a project Dockerfile the embedded debian base is used; got:\n%s", out)
	}
}

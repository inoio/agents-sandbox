package image

import (
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

func TestRenderDockerfileDefaultBase(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	out := RenderDockerfile(a, nil, false)
	s := string(out)
	for _, want := range []string{
		"FROM debian:trixie-slim",
		"iptables",
		"ARG OPENCODE_VERSION",
		"LABEL org.opencode-sandbox.agent=opencode",
		"OPENCODE_DISABLE_AUTOUPDATE=true",
		"node-v22.14.0-linux",
		"echo tool > /etc/opencode-sandbox/agent-source",
		"echo user > /etc/opencode-sandbox/agent-source",
		"groupadd -g \"$USER_GID\" dev",
		"LABEL org.opencode-sandbox.managed=true",
		"USER dev",
		"WORKDIR /workspace",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"nodesource", "AGENT_INSTALL_BLOCK", "docker-ce", "runner-base",
		"org.opencode-sandbox.opencode-version", "DOCKER_VERSION",
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
		"ARG DOCKER_VERSION=27.5.1",
		"download.docker.com/linux/static/stable/$(uname -m)/docker-${DOCKER_VERSION}.tgz",
		"echo tool > /etc/opencode-sandbox/docker-source",
		"echo user > /etc/opencode-sandbox/docker-source",
		`echo '{"storage-driver":"vfs"}' > /etc/docker/daemon.json`,
		"groupadd -f docker",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
	dindIdx := strings.Index(s, "DOCKER_VERSION")
	agentIdx := strings.Index(s, "LABEL org.opencode-sandbox.agent")
	if dindIdx < 0 || agentIdx < 0 || dindIdx > agentIdx {
		t.Error("dind block must come before the agent block")
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
	for _, want := range []string{"LABEL org.opencode-sandbox.agent=opencode", "WORKDIR /workspace"} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered Dockerfile missing %q", want)
		}
	}
}

func TestRenderDockerfileManagedBaseFrom(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM opencode-sandbox/runner-base:latest\nRUN apt-get install -y tree\n")
	out := RenderDockerfile(a, project, false)
	s := string(out)
	if !strings.Contains(s, "FROM debian:trixie-slim") {
		t.Error("managed FROM must be replaced with the embedded base tools block")
	}
	if strings.Contains(s, "opencode-sandbox/runner-base") {
		t.Error("managed base reference must be replaced, not kept")
	}
	if !strings.Contains(s, "RUN apt-get install -y tree") {
		t.Error("user body must be preserved after the managed FROM")
	}
}

func TestRenderDockerfileDindFromImpliesDind(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	project := []byte("FROM opencode-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	out := RenderDockerfile(a, project, false)
	if !strings.Contains(string(out), "DOCKER_VERSION") {
		t.Error("a runner-base-dind FROM must imply the dind block even without the flag")
	}
}

func TestReplaceFinalStageFrom(t *testing.T) {
	in := []byte("FROM opencode-sandbox/runner-base:latest\nRUN echo hi\n")
	got := string(replaceFinalStageFrom(in))
	if strings.Contains(got, "FROM opencode-sandbox/runner-base") {
		t.Errorf("replaceFinalStageFrom must remove the FROM line, got %q", got)
	}
	if !strings.Contains(got, "RUN echo hi") {
		t.Errorf("replaceFinalStageFrom must preserve the body, got %q", got)
	}
}

func TestReplaceFinalStageFromMultiStageUsesLastFrom(t *testing.T) {
	in := []byte("FROM debian:trixie-slim AS base\nFROM base AS final\nRUN echo hi\n")
	got := string(replaceFinalStageFrom(in))
	if strings.Contains(got, "FROM base AS final") {
		t.Errorf("replaceFinalStageFrom must remove only the final stage FROM, got %q", got)
	}
	if !strings.Contains(got, "FROM debian:trixie-slim AS base") {
		t.Errorf("replaceFinalStageFrom must keep earlier stages, got %q", got)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

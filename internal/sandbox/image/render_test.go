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
		"node-v26.8.1-linux",
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
		"ARG DOCKER_VERSION=29.7.2",
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
	devIdx := strings.Index(s, `groupadd -g "$USER_GID" dev`)
	dockerIdx := strings.Index(s, "groupadd -f docker")
	if devIdx < 0 || dockerIdx < 0 || devIdx > dockerIdx {
		t.Error("dev user must be created before the docker group is added")
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
	devIdx := strings.Index(s, `groupadd -g "$USER_GID" dev`)
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
	devIdx := strings.Index(s, `groupadd -g "$USER_GID" dev`)
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
		"FROM golang:1.24 AS build\nRUN go build -o /app .\nFROM opencode-sandbox/runner-base-dind:latest\nCOPY --from=build /app /app\n",
	)
	out := RenderDockerfile(a, project, false)
	s := string(out)
	buildFromIdx := strings.Index(s, "FROM golang:1.24 AS build")
	baseFromIdx := strings.Index(s, "FROM debian:trixie-slim")
	devIdx := strings.Index(s, `groupadd -g "$USER_GID" dev`)
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

func TestRenderDockerfileDevUserIsFirstInstruction(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	out := RenderDockerfile(a, nil, false)
	s := string(out)
	devIdx := strings.Index(s, `groupadd -g "$USER_GID" dev`)
	toolsIdx := strings.Index(s, "iptables")
	if devIdx < 0 || toolsIdx < 0 || devIdx > toolsIdx {
		t.Error("dev user block must be created before the base tools, as the first instruction of the final stage")
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
	block := []byte("FROM debian:trixie-slim\nRUN apt-get update\n")
	got := string(replaceFinalStageFrom(in, block))
	if strings.Contains(got, "FROM opencode-sandbox/runner-base") {
		t.Errorf("replaceFinalStageFrom must drop the managed FROM, got %q", got)
	}
	if !strings.Contains(got, "FROM debian:trixie-slim") {
		t.Errorf("replaceFinalStageFrom must substitute the replacement block, got %q", got)
	}
	if !strings.HasSuffix(got, "RUN echo hi\n") {
		t.Errorf("replaceFinalStageFrom must preserve the body after the block, got %q", got)
	}
}

func TestReplaceFinalStageFromMultiStageUsesLastFrom(t *testing.T) {
	in := []byte("FROM debian:trixie-slim AS base\nFROM base AS final\nRUN echo hi\n")
	block := []byte("FROM debian:trixie-slim\nRUN apt-get update\n")
	got := string(replaceFinalStageFrom(in, block))
	if strings.Contains(got, "FROM base AS final") {
		t.Errorf("replaceFinalStageFrom must replace only the final stage FROM, got %q", got)
	}
	if !strings.Contains(got, "FROM debian:trixie-slim AS base") {
		t.Errorf("replaceFinalStageFrom must keep earlier stages, got %q", got)
	}
	if !strings.Contains(got, "RUN echo hi") {
		t.Errorf("replaceFinalStageFrom must preserve the final-stage body, got %q", got)
	}
}

func TestInsertAfterLastFrom(t *testing.T) {
	in := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	block := []byte("RUN echo dev\n")
	got := string(insertAfterLastFrom(in, block))
	want := "FROM debian:trixie-slim\nRUN echo dev\nRUN echo hi\n"
	if got != want {
		t.Errorf("insertAfterLastFrom must insert block after the FROM, got %q", got)
	}
}

func TestInsertAfterLastFromMultiStage(t *testing.T) {
	in := []byte("FROM golang:1.24 AS build\nRUN go build\nFROM debian:trixie-slim AS final\nRUN echo hi\n")
	block := []byte("RUN echo dev\n")
	got := string(insertAfterLastFrom(in, block))
	want := "FROM golang:1.24 AS build\nRUN go build\nFROM debian:trixie-slim AS final\nRUN echo dev\nRUN echo hi\n"
	if got != want {
		t.Errorf("insertAfterLastFrom must insert after the final stage FROM, got %q", got)
	}
}

func TestInsertAfterLastFromNoFrom(t *testing.T) {
	in := []byte("RUN echo hi\n")
	got := string(insertAfterLastFrom(in, []byte("RUN echo dev\n")))
	if got != "RUN echo hi\n" {
		t.Errorf("insertAfterLastFrom without a FROM must return input unchanged, got %q", got)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

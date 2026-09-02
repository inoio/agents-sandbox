package image

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
)

// managedBaseRef and managedBaseDindRef are the pre-redesign base image
// references recognized in a project Dockerfile's final stage. They are
// replaced with the embedded base tools block for backward compatibility; the
// -dind variant also implies the dind block.
const (
	managedBaseRef     = "opencode-sandbox/runner-base"
	managedBaseDindRef = "opencode-sandbox/runner-base-dind"
)

// agentLabelKey is the image label carrying the baked agent name.
const agentLabelKey = "org.opencode-sandbox.agent"

// Pinned third-party versions baked into the image.
const (
	nodeVersion   = "v22.14.0"
	dockerVersion = "27.5.1"
)

// RenderDockerfile composes the single per-project runner Dockerfile from the
// agent, the project Dockerfile (if any), and the dind switch. Tool-owned
// blocks are appended after the base/user content; the base is either the
// embedded debian tools block (default, or after replacing a managed FROM) or
// the user's own custom base.
func RenderDockerfile(a agent.Agent, projectDockerfile []byte, dind bool) []byte {
	base := embeddedBaseToolsBlock

	switch {
	case len(bytes.TrimSpace(projectDockerfile)) == 0:
		// No project Dockerfile: the embedded debian base tools block is the whole base.
	case referencesImage(projectDockerfile, managedBaseRef) ||
		referencesImage(projectDockerfile, managedBaseDindRef):
		// Managed FROM: replace the final stage's FROM with the embedded base
		// tools block, keeping earlier build stages and the body in place.
		if referencesImage(projectDockerfile, managedBaseDindRef) {
			dind = true
		}
		base = replaceFinalStageFrom(projectDockerfile, embeddedBaseToolsBlock)
	default:
		// Custom base: keep the user's whole Dockerfile.
		base = projectDockerfile
	}

	var out strings.Builder
	out.Write(base)
	out.WriteString("\n")
	if dind {
		out.WriteString(dindBlock())
		out.WriteString("\n")
	}
	out.WriteString(agentBlock(a))
	out.WriteString("\n")
	out.WriteString(finalizeBlock())

	// Create the dev user as the first instruction of the final stage so its
	// UID/GID is reserved before any stage body or tool-owned block runs.
	return insertAfterLastFrom([]byte(out.String()), []byte(devUserBlock()))
}

// devUserBlock creates the dev user as root, leaving the shell as root.
func devUserBlock() string {
	return `USER root
ARG USER_UID=1000
ARG USER_GID=1000
RUN id -u dev >/dev/null 2>&1 || \
      { groupadd -g "$USER_GID" dev && useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash dev; }
`
}

// replaceFinalStageFrom swaps a project Dockerfile's final stage FROM for the
// given block (which carries its own FROM), keeping earlier build stages and
// the body that follows the FROM.
func replaceFinalStageFrom(dockerfile []byte, block []byte) []byte {
	lines := bytes.SplitAfter(dockerfile, []byte("\n"))
	lastFrom := lastFromLine(lines)
	if lastFrom < 0 {
		return dockerfile
	}
	var out bytes.Buffer
	out.Write(bytes.Join(lines[:lastFrom], nil))
	out.Write(block)
	if !bytes.HasSuffix(block, []byte("\n")) {
		out.WriteByte('\n')
	}
	out.Write(bytes.Join(lines[lastFrom+1:], nil))
	return out.Bytes()
}

// insertAfterLastFrom inserts block immediately after the last FROM
// instruction, making it the first instruction of the final stage.
func insertAfterLastFrom(dockerfile []byte, block []byte) []byte {
	lines := bytes.SplitAfter(dockerfile, []byte("\n"))
	lastFrom := lastFromLine(lines)
	if lastFrom < 0 {
		return dockerfile
	}
	var out bytes.Buffer
	out.Write(bytes.Join(lines[:lastFrom+1], nil))
	out.Write(block)
	out.Write(bytes.Join(lines[lastFrom+1:], nil))
	return out.Bytes()
}

// lastFromLine returns the index of the last FROM instruction in lines, or -1.
func lastFromLine(lines [][]byte) int {
	lastFrom := -1
	for i, line := range lines {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("FROM")) {
			lastFrom = i
		}
	}
	return lastFrom
}

// dindBlock returns the idempotent docker-engine install block, appended when
// dind is enabled (or implied by a runner-base-dind FROM). A base that already
// provides dockerd is deferred to (docker-source=user) but the vfs storage
// driver is still forced for microsandbox compatibility.
func dindBlock() string {
	return fmt.Sprintf(`USER root
ARG DOCKER_VERSION=%s

RUN set -e; mkdir -p /etc/opencode-sandbox && \
    if command -v dockerd >/dev/null 2>&1; then \
      echo user > /etc/opencode-sandbox/docker-source; \
    else \
      echo tool > /etc/opencode-sandbox/docker-source; \
      curl -fsSL "https://download.docker.com/linux/static/stable/$(uname -m)/docker-${DOCKER_VERSION}.tgz" \
        | tar -xz -C /usr/local/bin --strip-components=1; \
      for p in iptables git ps xz curl tar; do \
        command -v "$p" >/dev/null 2>&1 || \
          { echo "error: docker prerequisite missing: $p" >&2; exit 1; }; \
      done; \
    fi

# Microsandbox compatibility: always force the vfs storage driver, even for a
# user-provided dockerd.
RUN mkdir -p /etc/docker && \
    echo '{"storage-driver":"vfs"}' > /etc/docker/daemon.json
RUN groupadd -f docker
`, dockerVersion)
}

// agentBlock renders the idempotent node+agent install block. A base that
// already provides node or the agent is left untouched (provenance recorded).
func agentBlock(a agent.Agent) string {
	spec := a.ImageSpec()
	var envBlock strings.Builder
	for k, v := range spec.AgentEnv {
		fmt.Fprintf(&envBlock, "ENV %s=%s\n", k, v)
	}
	binary := a.Name()
	if provider, ok := agent.AsVersionProvider(a); ok {
		if fields := strings.Fields(provider.VersionCmd()); len(fields) > 0 {
			binary = fields[0]
		}
	}
	return fmt.Sprintf(`USER root
ARG %s
LABEL %s=%s
%sRUN command -v node >/dev/null 2>&1 || { \
      case "$(uname -m)" in \
        x86_64) NODE_ARCH=x64 ;; \
        aarch64) NODE_ARCH=arm64 ;; \
        *) echo "error: unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
      esac; \
      curl -fsSL "https://nodejs.org/dist/%s/node-%s-linux-${NODE_ARCH}.tar.gz" \
        | tar -xz -C /usr/local --strip-components=1; \
    }

RUN mkdir -p /etc/opencode-sandbox && \
    if command -v %s >/dev/null 2>&1; then \
      echo user > /etc/opencode-sandbox/agent-source; \
    else \
      echo tool > /etc/opencode-sandbox/agent-source; \
      %s; \
    fi
`,
		spec.VersionArg,
		agentLabelKey, a.Name(),
		envBlock.String(),
		nodeVersion, nodeVersion,
		binary,
		spec.InstallCommand,
	)
}

// finalizeBlock records the image contract labels and makes dev the runtime
// user. The dev user itself is created earlier by devUserBlock.
func finalizeBlock() string {
	return `USER root
ARG BASE_IMAGE

RUN usermod -aG docker dev 2>/dev/null || true
USER dev
WORKDIR /workspace
LABEL org.opencode-sandbox.managed=true
LABEL org.opencode-sandbox.base=$BASE_IMAGE
`
}

// readProjectDockerfile returns the project Dockerfile bytes, or nil when none
// exists.
func readProjectDockerfile() []byte {
	if data, err := os.ReadFile(configpaths.Get().ProjectDockerfile()); err == nil {
		return data
	}
	return nil
}

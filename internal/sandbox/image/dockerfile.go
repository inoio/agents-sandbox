package image

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// managedBaseRef and managedBaseDindRef are the pre-redesign base image
// references recognized in a project Dockerfile's final stage. They are
// replaced with the embedded base tools block for backward compatibility; the
// -dind variant also implies the dind block.
const (
	managedBaseRef     = "agents-sandbox/runner-base"
	managedBaseDindRef = "agents-sandbox/runner-base-dind"
)

// agentLabelKey is the image label carrying the baked agent name.
const agentLabelKey = "org.agents-sandbox.agent"

// dockerfileIDLabelKey is the image label carrying the content identity of the
// baked runner image, used to skip rebuilds when nothing that affects the
// image has changed.
const dockerfileIDLabelKey = "org.agents-sandbox.dockerfile-id"

// computeDockerfileID returns the content identity of a rendered runner
// Dockerfile combined with the pinned agent version, capturing every input
// that affects the baked image while excluding host-dependent build args.
func computeDockerfileID(rendered []byte, agentVersion string) string {
	h := sha256.Sum256(append(rendered, []byte(agentVersion)...))
	return hex.EncodeToString(h[:])
}

// Pinned third-party versions baked into the image.
const (
	nodeVersion   = "v26.8.1"
	dockerVersion = "29.7.2"
)

// dindMarker is the comment a project Dockerfile can place in its final stage to
// control where the Docker-in-Docker install block runs, e.g. after installing the
// dind prerequisites and before configuring docker.
const dindMarker = "# agents-sandbox:dind"

// RenderDockerfile composes the single per-project runner Dockerfile from the
// agent, the project Dockerfile (if any), and the dind switch. The project's
// final stage is split into its base and body, then the tool-owned blocks are
// concatenated around it.
func RenderDockerfile(a agent.Agent, projectDockerfile []byte, dind bool) []byte {
	if referencesImage(projectDockerfile, managedBaseDindRef) {
		dind = true
	}
	earlier, base, body := splitFinalStage(projectDockerfile)
	if dind {
		body = injectDindBlock(body, dindBlock())
	}
	return []byte(joinBlocks(
		earlier,
		base,
		devUserBlock(),
		body,
		dindFinalizationBlock(),
		agentBlock(a),
		finalizationBlock(),
	))
}

// splitFinalStage splits a project Dockerfile into the earlier build stages, the
// final stage's base, and the body after it. Without a project Dockerfile the
// embedded tools block is the whole base. A managed base has its final FROM
// replaced by the embedded tools block, keeping earlier stages; a custom base keeps
// its own FROM.
func splitFinalStage(projectDockerfile []byte) (string, string, string) {
	if len(bytes.TrimSpace(projectDockerfile)) == 0 {
		return "", string(embeddedBaseToolsBlock), ""
	}
	lines := bytes.SplitAfter(projectDockerfile, []byte("\n"))
	lastFrom := lastFromLine(lines)
	if lastFrom < 0 {
		return "", string(projectDockerfile), ""
	}
	earlier := string(bytes.Join(lines[:lastFrom], nil))
	body := string(bytes.Join(lines[lastFrom+1:], nil))
	if referencesImage(projectDockerfile, managedBaseRef) ||
		referencesImage(projectDockerfile, managedBaseDindRef) {
		return earlier, string(embeddedBaseToolsBlock), body
	}
	return earlier, string(lines[lastFrom]), body
}

// injectDindBlock replaces the first dind marker line in body with block, or
// appends block when no marker is present. body is the final stage's body, so a
// marker in an earlier build stage is never seen.
func injectDindBlock(body, block string) string {
	lines := strings.SplitAfter(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == dindMarker {
			return strings.Join(lines[:i], "") + block + strings.Join(lines[i+1:], "")
		}
	}
	return body + block
}

// joinBlocks concatenates non-empty blocks with a newline between them, so empty
// segments (earlier stages, the body, or the dind block) leave no blank lines.
func joinBlocks(blocks ...string) string {
	nonEmpty := blocks[:0]
	for _, block := range blocks {
		if strings.TrimSpace(block) != "" {
			nonEmpty = append(nonEmpty, block)
		}
	}
	return strings.Join(nonEmpty, "\n")
}

// devUserBlock creates the dev user as root, leaving the shell as root.
// groupadd -f tolerates a host GID already taken in the base image (e.g. macOS
// staff/20 vs dialout): the group then gets the next free GID, so useradd must
// reference it by name. The login shell prefers bash, then zsh, then sh, so a
// custom base need only provide a POSIX shell.
func devUserBlock() string {
	return `USER root
ARG USER_UID=1000
ARG USER_GID=1000
RUN id -u dev >/dev/null 2>&1 || \
      { groupadd -f -g "$USER_GID" dev && \
        p=/bin/sh; for sh in bash zsh sh; do q="$(command -v "$sh" 2>/dev/null)" && { p="$q"; break; }; done; \
        useradd -m -u "$USER_UID" -g dev -s "$p" dev; }
`
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
// provides dockerd is deferred to (docker-source=user).
func dindBlock() string {
	return fmt.Sprintf(`USER root
ARG DOCKER_VERSION=%s

RUN set -e; mkdir -p /etc/agents-sandbox && \
    if command -v dockerd >/dev/null 2>&1; then \
      echo user > /etc/agents-sandbox/docker-source; \
    else \
      echo tool > /etc/agents-sandbox/docker-source; \
      curl -fsSL "https://download.docker.com/linux/static/stable/$(uname -m)/docker-${DOCKER_VERSION}.tgz" \
        | tar -xz -C /usr/local/bin --strip-components=1; \
      for p in iptables git ps xz curl tar; do \
        command -v "$p" >/dev/null 2>&1 || \
          { echo "error: docker prerequisite missing: $p" >&2; exit 1; }; \
      done; \
    fi

RUN groupadd -f docker
`, dockerVersion)
}

// dindFinalizationBlock finalizes docker for both dind=true and a user-defined Dockerfile that installs docker.
func dindFinalizationBlock() string {
	return `USER root
# Microsandbox compatibility: always force the vfs storage driver
RUN mkdir -p /etc/docker && \
    echo '{"storage-driver":"vfs"}' > /etc/docker/daemon.json
# if docker group exists, add dev user to it
RUN usermod -aG docker dev 2>/dev/null || true
`
}

// agentBlock renders the idempotent node+agent install block. A base that
// already provides node or the agent is left untouched (provenance recorded).
func agentBlock(a agent.Agent) string {
	spec := a.ImageSpec()
	var envBlock strings.Builder
	envKeys := make([]string, 0, len(spec.AgentEnv))
	for k := range spec.AgentEnv {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		fmt.Fprintf(&envBlock, "ENV %s=%s\n", k, spec.AgentEnv[k])
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

RUN mkdir -p /etc/agents-sandbox && \
    if command -v %s >/dev/null 2>&1; then \
      echo user > /etc/agents-sandbox/agent-source; \
    else \
      echo tool > /etc/agents-sandbox/agent-source; \
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

// finalizationBlock records the image contract labels and makes dev the runtime
// user. The dev user itself is created earlier by devUserBlock. The
// dockerfile-id label lives here, after the agent install, so an agent upgrade
// does not invalidate the cached node and agent install layers.
func finalizationBlock() string {
	return `USER root
ARG BASE_IMAGE
ARG DOCKERFILE_ID

USER dev
# extend PATH with ~/.local/bin
ENV PATH="/home/dev/.local/bin:${PATH}"
WORKDIR /workspace
LABEL org.agents-sandbox.managed=true
LABEL org.agents-sandbox.base=$BASE_IMAGE
LABEL org.agents-sandbox.dockerfile-id=$DOCKERFILE_ID
`
}

// RenderProjectDockerfile renders the runner Dockerfile exactly as it would be
// built for the current project: the agent profile, the on-disk project
// Dockerfile (if any), and the dind switch. It is the single source of truth
// for previewing and building the image.
func RenderProjectDockerfile(a agent.Agent, dind bool) []byte {
	return RenderDockerfile(a, readProjectDockerfile(), dind)
}

// readProjectDockerfile returns the project Dockerfile bytes, or nil when none
// exists.
func readProjectDockerfile() []byte {
	if data, err := os.ReadFile(configpaths.Get().ProjectDockerfile()); err == nil {
		return data
	}
	return nil
}

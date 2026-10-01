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
// Dockerfile combined with the pinned agent version and the TLS interception CA
// certificate, capturing every input that affects the baked image while
// excluding host-dependent build args.
func computeDockerfileID(rendered []byte, agentVersion string, caCert []byte) string {
	h := sha256.New()
	h.Write(rendered)
	h.Write([]byte(agentVersion))
	h.Write(caCert)
	return hex.EncodeToString(h.Sum(nil))
}

// Pinned third-party versions baked into the image.
const (
	nodeVersion   = "v26.8.1"
	dockerVersion = "29.7.2"
)

// caContextFile is the name the TLS interception CA certificate is added to
// the docker build context under, referenced by caTrustBlock. Custom bases can
// COPY it into any stage of their own Dockerfile to trust the interceptor.
const caContextFile = "agents-sandbox-ca.crt"

// RenderDockerfile composes the single per-project runner Dockerfile from the
// agent, the project Dockerfile (if any), and the dind switch. Tool-owned
// blocks are appended after the base/user content; the base is either the
// embedded debian tools block (default, or after replacing a managed FROM) or
// the user's own custom base. Managed bases get the interception CA baked into
// the system trust store; custom bases are left untouched (the certificate is
// still shipped in the build context for their own COPY use).
func RenderDockerfile(a agent.Agent, projectDockerfile []byte, dind bool) []byte {
	var base []byte

	switch {
	case len(bytes.TrimSpace(projectDockerfile)) == 0:
		// No project Dockerfile: the embedded debian base tools block is the
		// whole base, immediately followed by the CA trust block so the
		// interception CA is trusted before the tool-owned HTTPS downloads.
		base = withTrustedCA(embeddedBaseToolsBlock)
	case referencesImage(projectDockerfile, managedBaseRef) ||
		referencesImage(projectDockerfile, managedBaseDindRef):
		// Managed FROM: replace the final stage's FROM with the embedded base
		// tools block, keeping earlier build stages and the body in place. The
		// CA trust block is inserted between the embedded tools and the user
		// body so build-time HTTPS steps in the body also trust the interceptor.
		if referencesImage(projectDockerfile, managedBaseDindRef) {
			dind = true
		}
		base = replaceFinalStageFrom(projectDockerfile, embeddedBaseToolsBlock, []byte(caTrustBlock()))
	default:
		// Custom base: keep the user's whole Dockerfile untouched; the CA cert
		// is still shipped in the build context for their own COPY use.
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

// withTrustedCA returns block followed by the CA trust block, installing the
// interception CA right after the base tools (which install ca-certificates)
// and before any HTTPS download.
func withTrustedCA(block []byte) []byte {
	out := make([]byte, 0, len(block)+len(caTrustBlock()))
	out = append(out, block...)
	return append(out, caTrustBlock()...)
}

// caTrustBlock installs the host's TLS interception CA into the system trust
// store so the VM and build-time HTTPS steps trust the interceptor. It is
// emitted only for managed bases, which guarantee the ca-certificates package.
// The certificate itself is provided by the build context.
func caTrustBlock() string {
	return `USER root
RUN mkdir -p /usr/local/share/ca-certificates
COPY ` + caContextFile + ` /usr/local/share/ca-certificates/microsandbox-ca.crt
RUN update-ca-certificates
`
}

// devUserBlock creates the dev user as root, leaving the shell as root.
// groupadd -f tolerates a host GID already taken in the base image (e.g. macOS
// staff/20 vs dialout): the group then gets the next free GID, so useradd must
// reference it by name.
func devUserBlock() string {
	return `USER root
ARG USER_UID=1000
ARG USER_GID=1000
RUN id -u dev >/dev/null 2>&1 || \
      { groupadd -f -g "$USER_GID" dev && useradd -m -u "$USER_UID" -g dev -s /bin/bash dev; }
`
}

// replaceFinalStageFrom swaps a project Dockerfile's final stage FROM for the
// given block (which carries its own FROM), inserting afterBlock immediately
// after that block, keeping earlier build stages and the body that follows.
func replaceFinalStageFrom(dockerfile, block, afterBlock []byte) []byte {
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
	out.Write(afterBlock)
	if len(afterBlock) > 0 && !bytes.HasSuffix(afterBlock, []byte("\n")) {
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
ARG DOCKERFILE_ID
LABEL org.agents-sandbox.dockerfile-id=$DOCKERFILE_ID
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

// finalizeBlock records the image contract labels and makes dev the runtime
// user. The dev user itself is created earlier by devUserBlock.
func finalizeBlock() string {
	return `USER root
ARG BASE_IMAGE

RUN usermod -aG docker dev 2>/dev/null || true
USER dev
WORKDIR /workspace
LABEL org.agents-sandbox.managed=true
LABEL org.agents-sandbox.base=$BASE_IMAGE
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

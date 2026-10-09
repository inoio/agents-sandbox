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

// managedBaseRef, managedBaseDindRef, and managedBaseDockerRef are the base
// image references recognized in a project Dockerfile's final stage. They are
// replaced with the embedded base tools block; the docker variants also imply
// docker support. The -dind spelling is the deprecated alias of -docker.
const (
	managedBaseRef       = "agents-sandbox/runner-base"
	managedBaseDindRef   = "agents-sandbox/runner-base-dind"
	managedBaseDockerRef = "agents-sandbox/runner-base-docker"
)

// Fixed, reserved stage aliases the renderer emits. They are deterministic
// (never uuid/hash-suffixed) because dockerfile-id is a content hash of the
// rendered Dockerfile. A project Dockerfile declaring one is a hard error.
const (
	baseStageAlias   = "agents-sandbox-base"
	nodeStageAlias   = "agents-sandbox-node"
	agentStageAlias  = "agents-sandbox-agent"
	dockerStageAlias = "agents-sandbox-docker"
	runnerStageAlias = "agents-sandbox-runner"
)

// reservedStageAliases is the set of stage aliases the renderer owns.
var reservedStageAliases = []string{
	baseStageAlias, nodeStageAlias, agentStageAlias, dockerStageAlias, runnerStageAlias,
}

// Tool artifact contract: every tool lands under toolDir and is exposed through
// toolBin, which the runner appends to PATH so a base-provided binary keeps
// precedence.
const (
	toolDir = "/opt/agents-sandbox"
	toolBin = "/opt/agents-sandbox/bin"
)

// pathMergeAsset is the build-context file carrying the profile.d merge script;
// pathMergeDest is where the final stage installs it inside the image.
const (
	pathMergeAsset = "agents-sandbox-path.sh"
	pathMergeDest  = "/etc/profile.d/agents-sandbox-path.sh"
)

// agentLabelKey is the image label carrying the baked agent name.
const agentLabelKey = "org.agents-sandbox.agent"

// dockerfileIDLabelKey is the image label carrying the content identity of the
// baked runner image, used to skip rebuilds when nothing that affects the
// image has changed.
const dockerfileIDLabelKey = "org.agents-sandbox.dockerfile-id"

// computeDockerfileID returns the content identity of a rendered runner
// Dockerfile combined with the pinned agent version and the profile.d merge
// script, capturing every input that affects the baked image while excluding
// host-dependent build args. The merge script travels in the build-context tar
// under an invariant COPY line, so its bytes must be folded in here for an edit
// to invalidate an existing image.
func computeDockerfileID(rendered []byte, agentVersion string) string {
	h := sha256.New()
	h.Write(rendered)
	h.Write(pathMergeScript())
	h.Write([]byte(agentVersion))
	return hex.EncodeToString(h.Sum(nil))
}

// Pinned third-party versions baked into the image.
const (
	nodeVersion   = "v26.8.1"
	dockerVersion = "29.7.2"
)

// dockerMarker and legacyDockerMarker are the comment lines a project
// Dockerfile can place in its final stage to control where the thin docker
// copy/adopt block runs. The dind spelling is the deprecated alias.
const (
	dockerMarker       = "# agents-sandbox:docker"
	legacyDockerMarker = "# agents-sandbox:dind"
)

// RenderDockerfile composes the per-project runner Dockerfile from per-tool
// multistage stages. Tool-owned layers cache independently of the user body,
// the base, and each other; the runner stage merges the tool trees with COPY
// after the user body. It returns an error when the project Dockerfile
// declares a reserved stage alias.
func RenderDockerfile(a agent.Agent, projectDockerfile []byte, docker bool) ([]byte, error) {
	if err := checkReservedAliases(projectDockerfile); err != nil {
		return nil, err
	}
	if referencesImage(projectDockerfile, managedBaseDindRef) ||
		referencesImage(projectDockerfile, managedBaseDockerRef) {
		docker = true
	}

	managed := len(bytes.TrimSpace(projectDockerfile)) == 0 || isManagedBase(projectDockerfile)
	earlier, baseFrom, body := splitFinalStage(projectDockerfile)

	var baseStage, baseAlias string
	if managed {
		baseStage, baseAlias = managedBaseStage(), baseStageAlias
	} else {
		baseStage, baseAlias = customBaseStage(baseFrom)
	}

	body, dockerInjected := injectDockerBlock(body, docker)

	blocks := make([]string, 0, 6)
	blocks = append(blocks, earlier, baseStage, nodeStage(baseAlias), agentStage(a))
	if docker {
		blocks = append(blocks, dockerStage(baseAlias))
	}
	blocks = append(blocks, runnerStage(a, baseAlias, body, docker, dockerInjected))
	return []byte(joinBlocks(blocks...)), nil
}

// checkReservedAliases rejects a project Dockerfile that declares one of the
// renderer's reserved stage aliases.
func checkReservedAliases(projectDockerfile []byte) error {
	_, stageBase := scanFromStages(projectDockerfile)
	reserved := make(map[string]struct{}, len(reservedStageAliases))
	for _, alias := range reservedStageAliases {
		reserved[strings.ToLower(alias)] = struct{}{}
	}
	var collided []string
	for alias := range stageBase {
		if _, ok := reserved[strings.ToLower(alias)]; ok {
			collided = append(collided, alias)
		}
	}
	if len(collided) == 0 {
		return nil
	}
	sort.Strings(collided)
	return fmt.Errorf("project Dockerfile declares reserved stage alias(es): %s", strings.Join(collided, ", "))
}

// isManagedBase reports whether the project Dockerfile's final stage uses one
// of the managed base references.
func isManagedBase(projectDockerfile []byte) bool {
	return referencesImage(projectDockerfile, managedBaseRef) ||
		referencesImage(projectDockerfile, managedBaseDindRef) ||
		referencesImage(projectDockerfile, managedBaseDockerRef)
}

// splitFinalStage splits a project Dockerfile into the earlier build stages, the
// final stage's base, and the body after it. Without a project Dockerfile the
// embedded tools block is the whole base. A managed base has its final FROM
// replaced by the embedded tools block, keeping earlier stages; a custom base
// keeps its own FROM.
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
	if isManagedBase(projectDockerfile) {
		return earlier, string(embeddedBaseToolsBlock), body
	}
	return earlier, string(lines[lastFrom]), body
}

// managedBaseStage renders the embedded tools block with its FROM given the
// reserved base alias.
func managedBaseStage() string {
	block := string(embeddedBaseToolsBlock)
	lines := strings.SplitAfter(block, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "FROM") {
		lines[0] = strings.TrimRight(lines[0], "\n") + " AS " + baseStageAlias + "\n"
	}
	return strings.Join(lines, "")
}

// customBaseStage gives the user's final FROM the base stage alias, reusing an
// alias the user already declared. It returns the stage and the alias the tool
// stages must start from.
func customBaseStage(fromLine string) (string, string) {
	line := strings.TrimRight(fromLine, "\n")
	_, alias := parseFrom(line)
	if alias == "" {
		return line + " AS " + baseStageAlias + "\n", baseStageAlias
	}
	return line + "\n", alias
}

// nodeStage renders the Node.js tool stage. It installs Node into toolDir only
// when the base does not already provide it.
func nodeStage(baseAlias string) string {
	return fmt.Sprintf(`FROM %s AS %s
USER root
RUN mkdir -p %s && \
    if command -v node >/dev/null 2>&1; then \
      :; \
    else \
      case "$(uname -m)" in \
        x86_64) NODE_ARCH=x64 ;; \
        aarch64) NODE_ARCH=arm64 ;; \
        *) echo "error: unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
      esac; \
      curl -fsSL "https://nodejs.org/dist/%s/node-%s-linux-${NODE_ARCH}.tar.gz" \
        | tar -xz -C %s --strip-components=1; \
    fi
ENV PATH="%s:${PATH}"
`, baseAlias, nodeStageAlias, toolBin, nodeVersion, nodeVersion, toolDir, toolBin)
}

// agentStage renders the agent tool stage. It installs the agent into toolDir
// only when the base does not already provide its binary. The stage starts from
// the node stage so the agent install can use whichever npm is on PATH. The
// agent ENV and label are not set here: this stage is not in the final image's
// FROM lineage, so Docker would drop them. They are emitted by
// agentImageConfigBlock in the runner stage instead.
func agentStage(a agent.Agent) string {
	spec := a.ImageSpec()
	return fmt.Sprintf(`FROM %s AS %s
USER root
ARG %s
RUN if command -v %s >/dev/null 2>&1; then \
      :; \
    else \
      %s; \
    fi
`,
		nodeStageAlias, agentStageAlias,
		spec.VersionArg,
		agentBinary(a),
		spec.InstallCommand,
	)
}

// dockerStage renders the optional Docker tool stage. It installs the static
// Docker binaries into toolBin only when the base does not already provide
// dockerd. It does not check the runtime prerequisites (iptables, git, ps, xz,
// curl, tar): the stage starts from the raw base, before the user body, so a
// body that installs them is not yet visible. The check runs in the final stage
// instead (dockerAdoptionBlock), after the body.
func dockerStage(baseAlias string) string {
	return fmt.Sprintf(`FROM %s AS %s
USER root
ARG DOCKER_VERSION=%s
RUN set -e; mkdir -p %s && \
    if command -v dockerd >/dev/null 2>&1; then \
      :; \
    else \
      curl -fsSL "https://download.docker.com/linux/static/stable/$(uname -m)/docker-${DOCKER_VERSION}.tgz" \
        | tar -xz -C %s --strip-components=1; \
    fi
`, baseAlias, dockerStageAlias, dockerVersion, toolBin, toolBin)
}

// runnerStage renders the final image stage: it creates the dev user, runs the
// user body without the tool trees available, merges the tool trees with COPY,
// records provenance, and finalizes the image contract.
func runnerStage(a agent.Agent, baseAlias, body string, docker, dockerInjected bool) string {
	parts := []string{
		fmt.Sprintf("FROM %s AS %s\n", baseAlias, runnerStageAlias),
		devUserBlock(),
		fmt.Sprintf("ENV PATH=\"${PATH}:%s\"\n", toolBin),
		body,
	}
	if docker && !dockerInjected {
		parts = append(parts, dockerMergeBlock())
	}
	parts = append(parts,
		fmt.Sprintf("COPY --from=%s %s %s\n", agentStageAlias, toolDir, toolDir),
		agentAdoptionBlock(a),
		agentImageConfigBlock(a),
		finalizationBlock(),
	)
	return joinBlocks(parts...)
}

// injectDockerBlock replaces the first docker marker line in body with the
// thin docker copy/adopt block, or appends it when no marker is present. It
// reports whether the block was injected at a marker.
func injectDockerBlock(body string, docker bool) (string, bool) {
	if !docker {
		return body, false
	}
	block := dockerMergeBlock()
	lines := strings.SplitAfter(body, "\n")
	for i, line := range lines {
		if marker := strings.TrimSpace(line); marker == dockerMarker || marker == legacyDockerMarker {
			return strings.Join(lines[:i], "") + block + strings.Join(lines[i+1:], ""), true
		}
	}
	return body, false
}

// dockerMergeBlock copies the docker tool tree into the runner and records its
// provenance, so steps after the marker see docker.
func dockerMergeBlock() string {
	return fmt.Sprintf("COPY --from=%s %s %s\n%s", dockerStageAlias, toolDir, toolDir, dockerAdoptionBlock())
}

// dockerAdoptionBlock records whether dockerd was provided by the base (user)
// or the tool, checks the static-install runtime prerequisites when the tool
// installed the engine, ensures the docker group exists, and adds dev to it so
// the dev user can reach the root:docker 0660 socket. It resets to root so a
// project body ending in a non-root USER cannot break the block, and it runs in
// the final stage after the user body so a base that installs the prerequisites
// in the body is accepted. PATH prefers base binaries, so a base-provided
// dockerd resolves outside toolDir; a base that ships its own engine is not
// required to carry the static-tarball prerequisites.
func dockerAdoptionBlock() string {
	return fmt.Sprintf(`USER root
RUN set -e; mkdir -p /etc/agents-sandbox; \
    bin="$(command -v dockerd 2>/dev/null || true)"; \
    case "$bin" in %s/*) echo tool ;; *) echo user ;; esac \
      > /etc/agents-sandbox/docker-source; \
    if [ "$(cat /etc/agents-sandbox/docker-source)" = tool ]; then \
      for p in iptables git ps xz curl tar; do \
        command -v "$p" >/dev/null 2>&1 || \
          { echo "error: docker prerequisite missing: $p" >&2; exit 1; }; \
      done; \
    fi; \
    groupadd -f docker; \
    usermod -aG docker dev 2>/dev/null || true
`, toolDir)
}

// agentAdoptionBlock records whether the agent was provided by the base (user)
// or the tool. It resets to root so a project body ending in a non-root USER
// cannot break the block. PATH prefers base binaries, so a base-provided agent
// resolves outside toolDir.
func agentAdoptionBlock(a agent.Agent) string {
	return fmt.Sprintf(`USER root
RUN set -e; mkdir -p /etc/agents-sandbox; \
    bin="$(command -v %s 2>/dev/null || true)"; \
    case "$bin" in %s/*) echo tool ;; *) echo user ;; esac \
      > /etc/agents-sandbox/agent-source
`, agentBinary(a), toolDir)
}

// agentImageConfigBlock renders the sorted ENV lines for the agent's ImageSpec
// AgentEnv plus the agent provenance LABEL. It is emitted in the final runner
// stage because Docker drops config directives set in non-final stages.
func agentImageConfigBlock(a agent.Agent) string {
	spec := a.ImageSpec()
	envKeys := make([]string, 0, len(spec.AgentEnv))
	for k := range spec.AgentEnv {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	var block strings.Builder
	for _, k := range envKeys {
		fmt.Fprintf(&block, "ENV %s=%s\n", k, spec.AgentEnv[k])
	}
	fmt.Fprintf(&block, "LABEL %s=%s\n", agentLabelKey, a.Name())
	return block.String()
}

// agentBinary returns the command name the agent installs.
func agentBinary(a agent.Agent) string {
	if provider, ok := agent.AsVersionProvider(a); ok {
		if fields := strings.Fields(provider.VersionCmd()); len(fields) > 0 {
			return fields[0]
		}
	}
	return a.Name()
}

// joinBlocks concatenates non-empty blocks with a newline between them, so empty
// segments (earlier stages or the body) leave no blank lines.
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

// finalizationBlock records the image contract labels and makes dev the runtime
// user. The dev user itself is created earlier by devUserBlock. The
// dockerfile-id label lives here, after the agent install, so an agent upgrade
// does not invalidate the cached node and agent install layers.
func finalizationBlock() string {
	return fmt.Sprintf(`USER root
COPY %s %s
ARG BASE_IMAGE
ARG DOCKERFILE_ID

USER dev
# extend PATH with ~/.local/bin
ENV PATH="/home/dev/.local/bin:${PATH}"
ENV AGENTS_SANDBOX_IMAGE_PATH="${PATH}"
WORKDIR /workspace
LABEL org.agents-sandbox.managed=true
LABEL org.agents-sandbox.base=$BASE_IMAGE
LABEL org.agents-sandbox.dockerfile-id=$DOCKERFILE_ID
`, pathMergeAsset, pathMergeDest)
}

// RenderProjectDockerfile renders the runner Dockerfile exactly as it would be
// built for the current project: the agent profile, the on-disk project
// Dockerfile (if any), and the docker switch. It is the single source of truth
// for previewing and building the image.
func RenderProjectDockerfile(a agent.Agent, docker bool) ([]byte, error) {
	return RenderDockerfile(a, readProjectDockerfile(), docker)
}

// readProjectDockerfile returns the project Dockerfile bytes, or nil when none
// exists.
func readProjectDockerfile() []byte {
	if data, err := os.ReadFile(configpaths.Get().ProjectDockerfile()); err == nil {
		return data
	}
	return nil
}

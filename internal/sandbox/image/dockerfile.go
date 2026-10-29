package image

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
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

// distroBaseRef is the distro image the managed base builds on, rendered into
// both the embedded managed-base tools block (data/Dockerfile) and the tool base
// the node/agent/docker stages build on. It is registry-qualified so a build
// never depends on the engine's short-name resolution configuration.
const distroBaseRef = "docker.io/library/debian:trixie-slim"

// Fixed, reserved stage aliases the renderer emits. They are deterministic
// (never uuid/hash-suffixed) because dockerfile-id is a content hash of the
// rendered Dockerfile. A project Dockerfile declaring one is a hard error.
const (
	baseStageAlias     = "agents-sandbox-base"
	toolbaseStageAlias = "agents-sandbox-toolbase"
	nodeStageAlias     = "agents-sandbox-node"
	agentStageAlias    = "agents-sandbox-agent"
	dockerStageAlias   = "agents-sandbox-docker"
	runnerStageAlias   = "agents-sandbox-runner"
)

// reservedStageAliases is the set of stage aliases the renderer owns.
var reservedStageAliases = []string{
	baseStageAlias, toolbaseStageAlias, nodeStageAlias, agentStageAlias, dockerStageAlias, runnerStageAlias,
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

// trustHelperAsset is the build-context file carrying the CA-trust helper;
// trustHelperDest is where the final stage installs it inside the image.
// /usr/local/bin is on the PATH of the standard bases, so the helper runs
// without a shell; a custom base that drops it needs the absolute path.
const (
	trustHelperAsset = "msb-trust.sh"
	trustHelperDest  = "/usr/local/bin/msb-trust"
)

// trustHelperLicenseAsset is the build-context file carrying the license of the
// vendored CA-trust helper; trustHelperLicenseDest installs it inside the image.
// Apache-2.0 section 4(a) requires a copy of the license to travel with the
// helper, and /usr/share/licenses is the conventional home for it.
const (
	trustHelperLicenseAsset = "msb-trust.LICENSE"
	trustHelperLicenseDest  = "/usr/share/licenses/msb-trust/LICENSE"
)

// hostOpenerShimAsset is the build-context file carrying the in-VM xdg-open
// shim. It is installed in /usr/local/bin, which precedes /usr/bin on PATH, so
// it also shadows a real xdg-open from a custom base: the VM has no desktop,
// and links must reach the host instead. hostOpenerShimAliases are the other
// names agents and libraries invoke to open a browser.
const (
	hostOpenerShimAsset = "xdg-open.sh"
	hostOpenerShimDest  = "/usr/local/bin/xdg-open"
)

var hostOpenerShimAliases = []string{"x-www-browser", "sensible-browser"}

// hostOpenerBlock installs the xdg-open shim and points BROWSER at it.
// claude-code consults BROWSER before its headless check, which otherwise
// refuses to open anything when DISPLAY is unset; codex's opener also tries
// BROWSER first. Setting BROWSER rather than DISPLAY keeps X clients in the VM
// from trying to reach a display that does not exist.
func hostOpenerBlock() string {
	var block strings.Builder
	fmt.Fprintf(&block, "COPY %s %s\n", hostOpenerShimAsset, hostOpenerShimDest)
	links := make([]string, 0, len(hostOpenerShimAliases))
	for _, alias := range hostOpenerShimAliases {
		links = append(links, fmt.Sprintf("ln -sf xdg-open %s", path.Join(path.Dir(hostOpenerShimDest), alias)))
	}
	fmt.Fprintf(&block, "RUN %s\n", strings.Join(links, " && "))
	block.WriteString("ENV BROWSER=\"xdg-open\"\n")
	return block.String()
}

// agentLabelKey is the image label carrying the baked agent name.
const agentLabelKey = "org.agents-sandbox.agent"

// dockerfileIDLabelKey is the image label carrying the content identity of the
// baked runner image, used to skip rebuilds when nothing that affects the
// image has changed.
const dockerfileIDLabelKey = "org.agents-sandbox.dockerfile-id"

// Image identity labels record the individual inputs that determine the content
// identity, so a rebuild can name which input changed. Tool-owned render
// templates are intentionally not represented: a mismatch with matching
// components is reported as a generic content change.
const (
	projectDockerfileHashLabelKey = "org.agents-sandbox.identity.dockerfile-hash"
	agentVersionLabelKey          = "org.agents-sandbox.identity.agent-version"
	dockerLabelKey                = "org.agents-sandbox.identity.docker"
)

// Build args carrying the identity components to the finalization block.
const (
	projectDockerfileHashArg = "IDENTITY_PROJECT_DOCKERFILE_HASH"
	agentVersionArg          = "IDENTITY_AGENT_VERSION"
	dockerArg                = "IDENTITY_DOCKER"
)

// identityNone normalizes an absent identity input (e.g. no project Dockerfile
// or no recorded agent version) so it round-trips through an image label.
const identityNone = "none"

// imageIdentity holds the individual inputs baked into the runner image. Values
// are normalized strings so they can be recorded as labels and compared back.
type imageIdentity struct {
	ProjectDockerfileHash string
	AgentVersion          string
	Docker                string
}

// computeImageIdentity derives the identity components from the build inputs.
func computeImageIdentity(projectDockerfile []byte, agentVersion string, docker bool) imageIdentity {
	return imageIdentity{
		ProjectDockerfileHash: hashOrNone(projectDockerfile),
		AgentVersion:          valueOrNone(agentVersion),
		Docker:                strconv.FormatBool(docker),
	}
}

// hashOrNone returns the SHA-256 of data, or identityNone when data is empty.
func hashOrNone(data []byte) string {
	if len(bytes.TrimSpace(data)) == 0 {
		return identityNone
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// valueOrNone returns s, or identityNone when s is empty.
func valueOrNone(s string) string {
	if s == "" {
		return identityNone
	}
	return s
}

// computeDockerfileID returns the content identity of the synthetic build
// context — the rendered runner Dockerfile and every build-context file — plus
// the pinned agent version, capturing every input that affects the baked image
// while excluding host-dependent build args. The files travel under invariant
// COPY lines, so their bytes and modes must be part of the identity for an edit
// to invalidate an existing image. The file list is shared with the tar writer
// so a new asset cannot be missed here.
func computeDockerfileID(rendered []byte, agentVersion string) string {
	h := sha256.New()
	h.Write(rendered)
	for _, file := range buildContextFiles() {
		h.Write([]byte(file.name))
		h.Write([]byte(strconv.FormatInt(file.mode, 8)))
		h.Write(file.content)
	}
	h.Write([]byte(agentVersion))
	return hex.EncodeToString(h.Sum(nil))
}

// Pinned third-party versions baked into the image. Node stays on the v24 line:
// the v26 tarballs gained a hard libatomic.so.1 dependency the managed base does
// not provide, while v24 (no libatomic on x86_64/arm64) satisfies every npm
// agent (#132).
const (
	nodeVersion   = "v24.22.1"
	dockerVersion = "29.7.2"
)

// dockerMarker and legacyDockerMarker are the comment lines a project
// Dockerfile can place in its final stage to control where the thin docker
// copy/adopt block runs. The dind spelling is the deprecated alias.
const (
	dockerMarker       = "# agents-sandbox:docker"
	legacyDockerMarker = "# agents-sandbox:dind"
)

// effectiveDocker reports whether the docker capability is enabled. An explicit
// config/flag/env setting wins in both directions; otherwise a project
// Dockerfile that extends the managed docker or legacy dind base, or that
// carries a docker injection marker in its final-stage body, enables it.
//
// The two content signals have different reach: the managed base is resolved
// through the final stage's base image (referencesImage), while the marker is
// scanned only in the final-stage body, so an earlier stage's marker is not a
// signal. Enablement matches the marker exactly (case-sensitive), while the
// near-miss detection is deliberately case-insensitive so a case variant is
// still surfaced instead of silently ignored.
func effectiveDocker(projectDockerfile []byte, docker options.DockerSetting) bool {
	if docker.Explicit {
		return docker.Enabled
	}
	if referencesImage(projectDockerfile, managedBaseDindRef) ||
		referencesImage(projectDockerfile, managedBaseDockerRef) {
		return true
	}
	_, _, body := splitFinalStage(projectDockerfile)
	return hasDockerMarker([]byte(body))
}

// DockerfileDiagnostics is the classified result for one project Dockerfile:
// the effective capability, its source, and at most one message per offending
// line. Pure; no ui.
type DockerfileDiagnostics struct {
	Enabled  bool
	Source   string   // "" | "config" | "Dockerfile marker" | "Dockerfile base"
	Messages []string // warnings and the suppression notice, in file order
}

// DiagnoseDockerfile classifies a project Dockerfile against the resolved
// docker setting. The capability line comes first when present, then one
// message per offending line in file order; a marker line yields exactly one
// message, so a legacy marker outside the final stage reports only the
// ignored-marker notice, never the deprecation spelling.
func DiagnoseDockerfile(project []byte, setting options.DockerSetting) DockerfileDiagnostics {
	_, _, finalBody := splitFinalStage(project)
	finalMarker := hasDockerMarker([]byte(finalBody))
	contentAsks := finalMarker ||
		referencesImage(project, managedBaseDindRef) ||
		referencesImage(project, managedBaseDockerRef)

	d := DockerfileDiagnostics{Enabled: effectiveDocker(project, setting), Source: "", Messages: nil}

	// Capability line first: the source when content enables, the suppression
	// notice when explicit config turns content off.
	switch {
	case setting.Explicit && !setting.Enabled && contentAsks:
		d.Source = "config"
		d.Messages = append(d.Messages, "docker: off (config); project Dockerfile asks for docker")
	case setting.Explicit:
		d.Source = "config"
	// finalMarker here implies d.Enabled: an explicit setting is handled above,
	// so d.Enabled can only be false from content, which finalMarker would set.
	case finalMarker:
		d.Source = "Dockerfile marker"
		d.Messages = append(d.Messages, "docker: on (project Dockerfile marker)")
	case d.Enabled:
		d.Source = "Dockerfile base"
		d.Messages = append(d.Messages, "docker: on (project Dockerfile base)")
	}

	// Base-image deprecation (whole file, instruction-aware).
	if referencesImage(project, managedBaseDindRef) {
		d.Messages = append(
			d.Messages,
			"project Dockerfile uses the deprecated agents-sandbox/runner-base-dind base image; switch to agents-sandbox/runner-base-docker",
		)
	}

	// Per-line marker scan: exactly one message per marker line. Heredoc bodies
	// are skipped, so marker-like text inside a copied file stays inert.
	lines := dockerfileLines(project)
	lastFrom := lastFromLine(lines)
	for _, i := range topLevelLineIndices(lines) {
		text := strings.TrimSpace(lineText(lines[i]))
		if marker, ok := dockerMarkerKind(text); ok {
			// The FROM line is not a marker line, so i == lastFrom never occurs.
			// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="a marker line is never the final FROM line, so i == lastFrom is unreachable"
			if i <= lastFrom {
				d.Messages = append(
					d.Messages,
					"project Dockerfile has a docker marker outside the final stage; it is ignored — move it into the final stage (after the last FROM)",
				)
				continue
			}
			if marker == legacyDockerMarker {
				d.Messages = append(
					d.Messages,
					"project Dockerfile uses the deprecated # agents-sandbox:dind marker; switch to # agents-sandbox:docker",
				)
			}
			continue
		}
		if nearMissDockerMarker(text) {
			d.Messages = append(d.Messages,
				fmt.Sprintf("project Dockerfile has a near-miss docker marker %q; use %q", text, dockerMarker))
		}
	}
	return d
}

// nearMissDockerMarker reports whether a trimmed line looks like an injection
// marker but is not the exact spelling: a comment naming the sandbox namespace
// and docker/dind. Exact match enables (case-sensitive); lenient detection
// warns (case-insensitive), so a case variant is still surfaced.
func nearMissDockerMarker(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	if _, ok := dockerMarkerKind(line); ok {
		return false
	}
	lower := strings.ToLower(line)
	return strings.Contains(lower, "agents-sandbox:") &&
		(strings.Contains(lower, "docker") || strings.Contains(lower, "dind"))
}

// EmitDockerfileDiagnostics writes every diagnostic message to the warn channel,
// keeping stdout free for the rendered Dockerfile.
func EmitDockerfileDiagnostics(ui termio.UI, d DockerfileDiagnostics) {
	for _, msg := range d.Messages {
		ui.Warn(msg)
	}
}

// EmitProjectDockerfileDiagnostics reads the on-disk project Dockerfile and
// emits its diagnostics. Convenience for callers that have no bytes in hand.
func EmitProjectDockerfileDiagnostics(ui termio.UI, setting options.DockerSetting) {
	EmitDockerfileDiagnostics(ui, DiagnoseDockerfile(readProjectDockerfile(), setting))
}

// dockerMarkerKind reports whether line, ignoring surrounding whitespace, is
// the current or legacy docker injection marker, returning the matched marker.
// It is the single place the marker comparison lives, shared by the warning
// scan and injectDockerBlock so the two cannot drift.
func dockerMarkerKind(line string) (string, bool) {
	marker := strings.TrimSpace(line)
	switch marker {
	case dockerMarker, legacyDockerMarker:
		return marker, true
	default:
		return "", false
	}
}

// hasDockerMarker reports whether any top-level line of data carries a docker
// injection marker, current or legacy. Heredoc bodies are skipped, so marker
// text copied into a file is not read as a marker.
func hasDockerMarker(data []byte) bool {
	lines := dockerfileLines(data)
	for _, i := range topLevelLineIndices(lines) {
		if _, ok := dockerMarkerKind(lineText(lines[i])); ok {
			return true
		}
	}
	return false
}

// RenderDockerfile composes the per-project runner Dockerfile from per-tool
// multistage stages. The node/agent/docker stages build on a tool-owned
// agents-sandbox-toolbase (pinned debian:trixie-slim plus curl, ca-certificates,
// and tar), not on the user's base, so a custom base needs neither curl nor tar
// and the tool layers cache across bases. Tool-owned layers cache independently
// of the user body, the base, and each other; the runner stage builds on the
// base stage and merges the tool trees with COPY after the user body.
//
// agentVersion is the resolved agent release baked into the agent stage's
// install ARG default. Every tool stage (node, agent, docker) states its pinned
// version as an ARG default in the rendered content, so a version bump changes
// the content identity and invalidates that stage's install layer even when the
// builder does not fold the version build arg into the cache key.
//
// It returns an error when the project Dockerfile declares a reserved stage
// alias.
func RenderDockerfile(
	a agent.Agent,
	agentVersion string,
	projectDockerfile []byte,
	docker options.DockerSetting,
) ([]byte, error) {
	if err := checkReservedAliases(projectDockerfile); err != nil {
		return nil, err
	}
	dockerEnabled := effectiveDocker(projectDockerfile, docker)

	managed := len(bytes.TrimSpace(projectDockerfile)) == 0 || isManagedBase(projectDockerfile)
	earlier, baseFrom, body := splitFinalStage(projectDockerfile)

	var baseStage, baseAlias string
	if managed {
		baseStage, baseAlias = managedBaseStage(), baseStageAlias
	} else {
		baseStage, baseAlias = customBaseStage(baseFrom)
	}

	body, dockerInjected := injectDockerBlock(body, dockerEnabled)

	blocks := make([]string, 0, 7)
	blocks = append(blocks, earlier, toolbaseStage())
	agentParent := toolbaseStageAlias
	if a.ImageSpec().RequiresNode {
		blocks = append(blocks, nodeStage())
		agentParent = nodeStageAlias
	}
	blocks = append(blocks, agentStage(a, agentParent, agentVersion))
	if dockerEnabled {
		blocks = append(blocks, dockerStage())
	}
	blocks = append(blocks, baseStage, runnerStage(a, baseAlias, body, dockerEnabled, dockerInjected))
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
// reserved base alias. The first-line check matches the FROM keyword
// case-insensitively.
func managedBaseStage() string {
	block := string(embeddedBaseToolsBlock)
	lines := strings.SplitAfter(block, "\n")
	if keyword, _ := instruction(lines[0]); keyword == fromInstruction {
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

// toolbaseStage renders the tool-owned stage the node/agent/docker stages build
// on. It carries only what the tool downloads need (curl, ca-certificates, tar)
// and is never part of the runner's lineage, so a custom base needs neither
// binary and the tool layers cache across bases. It shares the distroBaseRef
// layer with the managed base block.
func toolbaseStage() string {
	return fmt.Sprintf(`FROM %s AS %s
USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl tar && \
    rm -rf /var/lib/apt/lists/*
`, distroBaseRef, toolbaseStageAlias)
}

// nodeStage renders the Node.js tool stage on the tool base. It installs Node
// into toolDir unconditionally: the fixed tool base never carries node, and a
// base-provided node is handled by the runner's PATH precedence and recorded by
// adoption. The pinned version is the ARG default so a bump changes the rendered
// content and invalidates this stage.
func nodeStage() string {
	return fmt.Sprintf(`FROM %s AS %s
USER root
ARG NODE_VERSION=%s
RUN mkdir -p %s && \
    case "$(uname -m)" in \
      x86_64) NODE_ARCH=x64 ;; \
      aarch64) NODE_ARCH=arm64 ;; \
      *) echo "error: unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://nodejs.org/dist/${NODE_VERSION}/node-${NODE_VERSION}-linux-${NODE_ARCH}.tar.gz" \
      | tar -xz -C %s --strip-components=1
ENV PATH="%s:${PATH}"
`, toolbaseStageAlias, nodeStageAlias, nodeVersion, toolDir, toolDir, toolBin)
}

// agentStage renders the agent tool stage on parentAlias (the node stage when
// the agent needs node, otherwise the tool base). It installs the agent into
// toolDir unconditionally: the fixed parent never carries the agent, and a
// base-provided agent is handled by the runner's PATH precedence and recorded by
// adoption. It creates toolBin itself so the runner's COPY source exists without
// the node stage. The agent ENV and label are not set here: this stage is not in
// the final image's FROM lineage, so Docker would drop them. They are emitted by
// agentImageConfigBlock in the runner stage instead.
//
// The resolved version is the install ARG's default, so a version bump changes
// the rendered content and invalidates the install layer even when the builder
// does not fold the version build arg into the cache key. An empty version
// (e.g. a preview) leaves the ARG without a default.
func agentStage(a agent.Agent, parentAlias, agentVersion string) string {
	spec := a.ImageSpec()
	versionArg := fmt.Sprintf("ARG %s", spec.VersionArg)
	if agentVersion != "" {
		versionArg = fmt.Sprintf("ARG %s=%s", spec.VersionArg, agentVersion)
	}
	return fmt.Sprintf(`FROM %s AS %s
USER root
%s
RUN mkdir -p %s && %s
`,
		parentAlias, agentStageAlias,
		versionArg,
		toolBin,
		spec.InstallCommand,
	)
}

// dockerStage renders the optional Docker tool stage on the tool base. It
// installs the static Docker binaries into toolBin unconditionally: the fixed
// tool base never carries dockerd, and a base-provided engine is handled by the
// runner's PATH precedence and recorded by adoption. It does not check the
// runtime prerequisites (iptables, git, ps, xz): the check runs in the final
// stage instead (dockerAdoptionBlock), after the user body, so a body that
// installs them is accepted.
func dockerStage() string {
	return fmt.Sprintf(`FROM %s AS %s
USER root
ARG DOCKER_VERSION=%s
RUN set -e; mkdir -p %s && \
    curl -fsSL "https://download.docker.com/linux/static/stable/$(uname -m)/docker-${DOCKER_VERSION}.tgz" \
      | tar -xz -C %s --strip-components=1
`, toolbaseStageAlias, dockerStageAlias, dockerVersion, toolBin, toolBin)
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
		agentOwnershipBlock(a),
		agentImageConfigBlock(a),
		finalizationBlock(),
		guestToolsBlock(a),
		runtimeLibsBlock(a),
		launcherToolsBlock(),
	)
	return joinBlocks(parts...)
}

// agentOwnershipBlock hands the agent's self-written tool-tree paths to the
// runtime user. It runs as root after the tool tree is copied in and before the
// image switches to the dev user. The group is resolved at build time from the
// dev user's own entry (`id -g dev`), not assumed to be a `dev` group nor taken
// from the USER_UID/USER_GID build args: devUserBlock skips creation entirely
// when a custom base already provides a `dev` user, which need not come with a
// `dev` group, and groupadd -f may leave the dev group on a GID other than a
// pinned host GID. Agents that declare no owned paths emit nothing.
func agentOwnershipBlock(a agent.Agent) string {
	paths := a.ImageSpec().OwnedToolPaths
	if len(paths) == 0 {
		return ""
	}
	return fmt.Sprintf("USER root\nRUN chown -R dev:$(id -g dev) %s\n", strings.Join(paths, " "))
}

// injectDockerBlock replaces the first top-level docker marker line in body
// with the thin docker copy/adopt block, or appends it when no marker is
// present. A marker inside a heredoc body is not a marker and is left untouched.
// It reports whether the block was injected at a marker.
func injectDockerBlock(body string, docker bool) (string, bool) {
	if !docker {
		return body, false
	}
	block := dockerMergeBlock()
	lines := dockerfileLines([]byte(body))
	for _, i := range topLevelLineIndices(lines) {
		if _, ok := dockerMarkerKind(lineText(lines[i])); ok {
			return string(bytes.Join(lines[:i], nil)) + block + string(bytes.Join(lines[i+1:], nil)), true
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
// required to carry the static-tarball prerequisites. The download's curl/tar
// are not checked: the docker install runs on the tool base, which carries them.
func dockerAdoptionBlock() string {
	return fmt.Sprintf(`USER root
RUN set -e; mkdir -p /etc/agents-sandbox; \
    bin="$(command -v dockerd 2>/dev/null || true)"; \
    case "$bin" in %s/*) echo tool ;; *) echo user ;; esac \
      > /etc/agents-sandbox/docker-source; \
    if [ "$(cat /etc/agents-sandbox/docker-source)" = tool ]; then \
      for p in iptables git ps xz; do \
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

// launcherGuestTools are in-VM tools the launcher invokes directly, independent
// of the agent: curl replays a browser-login callback inside the VM for the
// host opener. The opener is on by default, so every runner image verifies
// these; the check makes a custom base that lacks one fail the image build
// naming the tool instead of failing at login time. The managed base already
// provides curl.
var launcherGuestTools = []string{"curl"}

// launcherToolsBlock verifies the in-VM tools the launcher's own features need,
// on top of the agent's runtime tools. It runs last, with the same PATH as
// guestToolsBlock, and fails the build naming the missing tool.
func launcherToolsBlock() string {
	return guestToolCheck(launcherGuestTools, "launcher runtime tool missing", "required by the host opener")
}

// guestToolsBlock verifies the in-VM tools this agent's runtime needs (daemon
// health, session and worktree calls, notifications, and process control). It
// runs last, as the runtime user and after finalizationBlock finalizes the
// image PATH (which adds ~/.local/bin), so it sees the same PATH the launcher's
// commands will; a tool installed only there is still found. It fails the build
// naming the missing tool instead of failing at runtime. Agents that run no
// daemon declare no tools and emit nothing.
func guestToolsBlock(a agent.Agent) string {
	requirer, ok := agent.AsGuestToolRequirer(a)
	if !ok {
		return ""
	}
	return guestToolCheck(requirer.GuestTools(), "agent runtime tool missing", "required by "+a.Name())
}

// guestToolCheck renders the RUN loop that fails the build when any of tools is
// absent from the image, naming the missing tool and why it is needed.
func guestToolCheck(tools []string, missing, reason string) string {
	if len(tools) == 0 {
		return ""
	}
	return fmt.Sprintf(`RUN set -e; for p in %s; do \
      command -v "$p" >/dev/null 2>&1 || \
        { echo "error: %s: $p (%s)" >&2; exit 1; }; \
    done
`, strings.Join(tools, " "), missing, reason)
}

// runtimeLibsBlock verifies that the shared libraries an agent's provisioned
// runtime links are resolvable in the final image, failing the build naming the
// library otherwise. It is the shared-library counterpart of guestToolsBlock:
// the agent's runtime is installed in the agent stage on the tool base, while
// the runner copies only the tool tree into the project's base, so a system
// library the runtime links is absent unless the base provides it. It runs after
// the body, so a base that installs the library in the body is accepted, and it
// is glibc-only because the requirement is a glibc one, so a musl base is
// skipped rather than failed. Agents that declare no libraries emit nothing.
func runtimeLibsBlock(a agent.Agent) string {
	libs := a.ImageSpec().RuntimeLibs
	if len(libs) == 0 {
		return ""
	}
	return fmt.Sprintf(`RUN set -e; if getconf GNU_LIBC_VERSION >/dev/null 2>&1; then \
      for lib in %s; do \
        ldconfig -p 2>/dev/null | grep -q "$lib" || \
          { echo "error: agent runtime library missing: $lib (required by %s; the base image must provide it)" >&2; exit 1; }; \
      done; \
    fi
`, strings.Join(libs, " "), a.Name())
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
// Only real instruction starts are considered, so a FROM inside a heredoc body
// or a continuation line is not a stage header; keywords are matched
// case-insensitively.
func lastFromLine(lines [][]byte) int {
	lastFrom := -1
	for _, i := range instructionLineIndices(lines) {
		if keyword, _ := instruction(string(lines[i])); keyword == fromInstruction {
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
COPY %s %s
COPY %s %s
%sARG BASE_IMAGE
ARG DOCKERFILE_ID
ARG IDENTITY_PROJECT_DOCKERFILE_HASH
ARG IDENTITY_AGENT_VERSION
ARG IDENTITY_DOCKER

USER dev
# extend PATH with ~/.local/bin
ENV PATH="/home/dev/.local/bin:${PATH}"
ENV AGENTS_SANDBOX_IMAGE_PATH="${PATH}"
WORKDIR /workspace
LABEL org.agents-sandbox.managed=true
LABEL org.agents-sandbox.base=$BASE_IMAGE
LABEL org.agents-sandbox.dockerfile-id=$DOCKERFILE_ID
LABEL org.agents-sandbox.identity.dockerfile-hash=$IDENTITY_PROJECT_DOCKERFILE_HASH
LABEL org.agents-sandbox.identity.agent-version=$IDENTITY_AGENT_VERSION
LABEL org.agents-sandbox.identity.docker=$IDENTITY_DOCKER
`, pathMergeAsset, pathMergeDest, trustHelperAsset, trustHelperDest, trustHelperLicenseAsset, trustHelperLicenseDest,
		hostOpenerBlock())
}

// RenderProjectDockerfile renders the runner Dockerfile exactly as it would be
// built for the current project: the agent profile, the pinned agent version,
// the on-disk project Dockerfile (if any), and the docker switch. It is the
// single source of truth for previewing and building the image. An empty
// version renders the agent instance stage without a pinned default.
func RenderProjectDockerfile(a agent.Agent, agentVersion string, docker options.DockerSetting) ([]byte, error) {
	return RenderDockerfile(a, agentVersion, readProjectDockerfile(), docker)
}

// readProjectDockerfile returns the project Dockerfile bytes, or nil when none
// exists.
func readProjectDockerfile() []byte {
	if data, err := os.ReadFile(configpaths.Get().ProjectDockerfile()); err == nil {
		return data
	}
	return nil
}

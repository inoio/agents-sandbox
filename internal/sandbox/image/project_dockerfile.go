package image

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

// parsedProjectDockerfile is the single parse of a project's on-disk Dockerfile.
// The renderer, the diagnostic classifier, and the image identity all read this
// value instead of re-lexing the bytes, so the final-stage split, the marker
// position, the docker enablement, and the recorded identity cannot disagree.
//
// The Dockerfile lexer itself lives in dockerfile_lines.go and instructions.go;
// this type holds only the derived results.
type parsedProjectDockerfile struct {
	// raw is the project Dockerfile's bytes; the image identity hashes them.
	raw []byte
	// earlier is every stage before the final FROM, verbatim.
	earlier string
	// finalBase is the final stage's base: the embedded tools block for an
	// empty or managed Dockerfile, otherwise the literal final FROM line.
	finalBase string
	// body is the final stage's text after its FROM line, verbatim.
	body string
	// bodyLines is body split into lines the way dockerfileLines splits it.
	bodyLines [][]byte
	// lines is raw split into lines.
	lines [][]byte
	// topLevelLines is raw's top-level line indices (comments, blank lines, and
	// instruction starts), shared by the diagnostic scan.
	topLevelLines []int
	// lastFrom is the index in lines of the last FROM instruction, or -1.
	lastFrom int
	// stageBase maps each declared stage alias to the image token it builds on.
	stageBase map[string]string
	// finalRef is the final stage's base resolved through the stage aliases.
	finalRef string
	// managed reports whether the final stage uses a managed base reference.
	managed bool
	// dockerBase reports whether the final stage's base is the managed docker
	// or the legacy dind base.
	dockerBase bool
	// dindBase reports whether the final stage's base is the legacy dind base.
	dindBase bool
	// marker is the first top-level docker marker in body, or "" when none.
	marker string
	// markerAt is the index in bodyLines of marker, or -1.
	markerAt int
}

// parseProjectDockerfile lexes the project Dockerfile once and derives every
// signal the renderer, the diagnostic classifier, and the image identity read.
func parseProjectDockerfile(data []byte) parsedProjectDockerfile {
	var p parsedProjectDockerfile
	p.raw = data
	p.lines = dockerfileLines(data)
	var instructionLines []int
	instructionLines, p.topLevelLines = dockerfileLineIndices(p.lines)
	// lastFrom is only ever compared with < 0 and used as a non-negative slice
	// bound, so any negative sentinel is equivalent.
	// gomutants:disable-next-line INTEGER_INCREMENT reason="lastFrom is only compared with < 0 and sliced from a non-negative index so -1 and -2 are indistinguishable"
	p.lastFrom = -1
	for _, i := range instructionLines {
		if keyword, _ := instruction(string(p.lines[i])); keyword == fromInstruction {
			p.lastFrom = i
		}
	}
	lastImage, stageBase := scanFromStagesFrom(p.lines, instructionLines)
	p.stageBase = stageBase
	p.finalRef = resolveStageBase(lastImage, stageBase)

	p.managed = len(bytes.TrimSpace(data)) == 0 ||
		p.referencesImage(managedBaseRef) ||
		p.referencesImage(managedBaseDindRef) ||
		p.referencesImage(managedBaseDockerRef)
	p.dindBase = p.referencesImage(managedBaseDindRef)
	p.dockerBase = p.dindBase || p.referencesImage(managedBaseDockerRef)

	switch {
	case len(bytes.TrimSpace(data)) == 0:
		p.finalBase = string(embeddedBaseToolsBlock)
	case p.lastFrom < 0:
		p.finalBase = string(data)
	default:
		p.earlier = string(bytes.Join(p.lines[:p.lastFrom], nil))
		p.body = string(bytes.Join(p.lines[p.lastFrom+1:], nil))
		if p.managed {
			p.finalBase = string(embeddedBaseToolsBlock)
		} else {
			p.finalBase = string(p.lines[p.lastFrom])
		}
	}

	p.bodyLines = dockerfileLines([]byte(p.body))
	bodyStart := p.lastFrom + 1
	if p.lastFrom < 0 {
		// Without a final FROM there is no body, so no line can be its marker;
		// start the scan at the end of the file, where nothing qualifies.
		bodyStart = len(p.lines)
	}
	p.markerAt = firstMarkerIndex(p.lines, p.topLevelLines, bodyStart)
	if p.markerAt >= 0 {
		p.marker, _ = dockerMarkerKind(lineText(p.bodyLines[p.markerAt]))
	}
	return p
}

// referencesImage reports whether the final stage's base refers to imageRef,
// ignoring any tag or digest on the reference.
func (p parsedProjectDockerfile) referencesImage(imageRef string) bool {
	return imageRefMatches(p.finalRef, imageRef)
}

// asksDocker reports whether the project Dockerfile's content asks for docker:
// its final stage builds on the managed docker or legacy dind base, or its
// final-stage body carries a marker. It is independent of the resolved setting.
func (p parsedProjectDockerfile) asksDocker() bool {
	return p.marker != "" || p.dockerBase
}

// effectiveDocker reports whether the docker capability is enabled: an explicit
// config/flag/env setting wins in both directions, otherwise the content
// signals decide.
func (p parsedProjectDockerfile) effectiveDocker(setting options.DockerSetting) bool {
	if setting.Explicit {
		return setting.Enabled
	}
	return p.asksDocker()
}

// diagnostics classifies the project Dockerfile against the resolved docker
// setting: the effective capability, its source, and at most one message per
// offending line, in file order.
func (p parsedProjectDockerfile) diagnostics(setting options.DockerSetting) DockerfileDiagnostics {
	contentAsks := p.asksDocker()
	enabled := p.effectiveDocker(setting)

	d := DockerfileDiagnostics{Enabled: enabled, Source: "", Messages: nil}

	// Capability line first: the source when content enables, the suppression
	// notice when explicit config turns content off.
	switch {
	case setting.Explicit && !setting.Enabled && contentAsks:
		d.Source = "config"
		d.Messages = append(d.Messages, "docker: off (config); project Dockerfile asks for docker")
	case setting.Explicit:
		d.Source = "config"
	// A body marker here implies enabled: an explicit setting is handled above,
	// so enabled can only be false from content, which a body marker would set.
	case p.marker != "":
		d.Source = "Dockerfile marker"
		d.Messages = append(d.Messages, "docker: on (project Dockerfile marker)")
	case enabled:
		d.Source = "Dockerfile base"
		d.Messages = append(d.Messages, "docker: on (project Dockerfile base)")
	}

	// Base-image deprecation (whole file, instruction-aware).
	if p.dindBase {
		d.Messages = append(
			d.Messages,
			"project Dockerfile uses the deprecated agents-sandbox/runner-base-dind base image; switch to agents-sandbox/runner-base-docker",
		)
	}

	// Per-line marker scan: exactly one message per marker line. Heredoc bodies
	// are skipped, so marker-like text inside a copied file stays inert.
	for _, i := range p.topLevelLines {
		text := strings.TrimSpace(lineText(p.lines[i]))
		if marker, ok := dockerMarkerKind(text); ok {
			// The FROM line is not a marker line, so i == lastFrom never occurs.
			// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="a marker line is never the final FROM line, so i == lastFrom is unreachable"
			if i <= p.lastFrom {
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

// reservedAliasError rejects a project Dockerfile that declares one of the
// renderer's reserved stage aliases.
func (p parsedProjectDockerfile) reservedAliasError() error {
	reserved := make(map[string]struct{}, len(reservedStageAliases))
	for _, alias := range reservedStageAliases {
		reserved[strings.ToLower(alias)] = struct{}{}
	}
	var collided []string
	for alias := range p.stageBase {
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

// render composes the per-project runner Dockerfile from per-tool multistage
// stages. The node/agent/docker stages build on a tool-owned
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
func (p parsedProjectDockerfile) render(
	a agent.Agent,
	agentVersion string,
	docker options.DockerSetting,
) ([]byte, error) {
	if err := p.reservedAliasError(); err != nil {
		return nil, err
	}
	return p.renderValid(a, agentVersion, docker), nil
}

// renderValid renders a project Dockerfile that declares no reserved stage
// alias, the only reason render fails. That depends on the project Dockerfile
// alone, not on the agent version or docker setting, so a re-render of an
// accepted project cannot fail.
func (p parsedProjectDockerfile) renderValid(
	a agent.Agent,
	agentVersion string,
	docker options.DockerSetting,
) []byte {
	dockerEnabled := p.effectiveDocker(docker)

	var baseStage, baseAlias string
	if p.managed {
		baseStage, baseAlias = managedBaseStage(), baseStageAlias
	} else {
		baseStage, baseAlias = customBaseStage(p.finalBase)
	}

	body, dockerInjected := injectDockerBlock(p.bodyLines, p.markerAt, dockerEnabled)

	// Resolve the profile's capabilities once for the whole render, so the
	// block builders read them instead of each re-asserting an interface.
	resolved := agent.Resolve(a)

	// The slice is built by appending, so no length or capacity hint is needed;
	// joinBlocks filters the empty blocks anyway.
	var blocks []string
	blocks = append(blocks, p.earlier, toolbaseStage())
	agentParent := toolbaseStageAlias
	if resolved.Profile.ImageSpec().RequiresNode {
		blocks = append(blocks, nodeStage())
		agentParent = nodeStageAlias
	}
	blocks = append(blocks, agentStage(a, agentParent, agentVersion))
	if dockerEnabled {
		blocks = append(blocks, dockerStage())
	}
	blocks = append(blocks, baseStage, runnerStage(resolved, baseAlias, body, dockerEnabled, dockerInjected))
	return []byte(joinBlocks(blocks...))
}

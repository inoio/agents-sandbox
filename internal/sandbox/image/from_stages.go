package image

import (
	"strings"

	"github.com/distribution/reference"
)

// scanFromStages walks a Dockerfile's FROM instructions, returning the final
// stage's image token and the declared stage-alias map. Only real instruction
// starts are read, so a stage header is not found inside a heredoc body or a
// continuation line; keywords are matched case-insensitively, so lowercase and
// mixed-case FROM lines are recognised.
func scanFromStages(dockerfile []byte) (string, map[string]string) {
	lines := dockerfileLines(dockerfile)
	return scanFromStagesFrom(lines, instructionLineIndices(lines))
}

// scanFromStagesFrom is scanFromStages over pre-lexed input, so a caller that
// already holds the line split and instruction indices (parseProjectDockerfile)
// does not re-lex. lines and instructionLines must come from the same Dockerfile.
func scanFromStagesFrom(lines [][]byte, instructionLines []int) (string, map[string]string) {
	stageBase := make(map[string]string)
	var lastImage string
	for _, i := range instructionLines {
		fromImage, stageAlias := parseFrom(string(lines[i]))
		if fromImage == "" {
			continue
		}
		if stageAlias != "" {
			stageBase[stageAlias] = fromImage
		}
		lastImage = fromImage
	}
	return lastImage, stageBase
}

// resolveStageBase follows a FROM image token through any declared stage
// aliases until it reaches the underlying image reference. A project
// Dockerfile controls its own aliases, so it can declare a cycle (a stage named
// after its own base image, or two stages that alias each other); the walk
// stops once a token repeats instead of following the cycle forever.
func resolveStageBase(token string, stageBase map[string]string) string {
	seen := make(map[string]struct{}, len(stageBase))
	for {
		if _, ok := seen[token]; ok {
			return token
		}
		seen[token] = struct{}{}
		base, ok := stageBase[token]
		if !ok {
			return token
		}
		token = base
	}
}

// imageRefMatches reports whether the given image token refers to imageRef,
// ignoring any tag or digest on the token.
func imageRefMatches(token, imageRef string) bool {
	ref, err := reference.ParseNormalizedNamed(token)
	if err != nil {
		return false
	}
	return reference.FamiliarName(ref) == imageRef
}

// parseFrom parses a Dockerfile FROM line, returning the main image reference
// and the declared stage alias (the "AS <name>" token, if present). The FROM and
// AS keywords are matched case-insensitively and leading build flags (e.g.
// --platform) are ignored. Trailing whitespace is trimmed so a CRLF line's CR
// does not leak into the alias. A line that is not a FROM instruction yields
// empty values.
func parseFrom(line string) (string, string) {
	keyword, rest := instruction(strings.TrimRight(line, " \t\r\n"))
	if keyword != fromInstruction {
		return "", ""
	}
	var fromImage string
	for rest != "" {
		token, tail := nextField(rest)
		rest = tail
		if strings.HasPrefix(token, "--") {
			continue
		}
		if fromImage == "" {
			fromImage = token
			continue
		}
		if strings.EqualFold(token, "AS") {
			if alias, _ := nextField(rest); alias != "" {
				return fromImage, alias
			}
		}
	}
	return fromImage, ""
}

// nextField splits off the first whitespace-delimited field of s, returning the
// field and the remainder of the string.
func nextField(s string) (string, string) {
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i+1:], " \t")
}

// baseImageRef returns the image reference the rendered Dockerfile's final stage
// builds from — the token the engine resolves for that FROM, through any stage
// aliases. It is deliberately returned unchanged: the launcher's base-digest
// lookup and the build must name the same image, and only the engine knows which
// registry a name without one maps to (podman's unqualified-search-registries,
// a registry mirror, or a prefix rewrite). Qualifying it here would send a user's
// own base to Docker Hub even when the engine resolves that name elsewhere. The
// tool-owned distro base is qualified where it is rendered (distroBaseRef).
func baseImageRef(rendered []byte) string {
	return finalStageToken(rendered)
}

// finalStageToken resolves the rendered Dockerfile's final stage FROM through
// any stage aliases to the underlying image reference.
func finalStageToken(rendered []byte) string {
	lastImage, stageBase := scanFromStages(rendered)
	return resolveStageBase(lastImage, stageBase)
}

package naming

import (
	"strconv"
	"strings"
)

// ArtifactInfo is the result of an artifact name parse operation. Task is true
// for a transient task sandbox, whose name ends in an all-digit nanos segment.
// ContentID carries the content id of a content-addressed runner image
// reference; it is empty for every other artifact.
type ArtifactInfo struct {
	Slug      string
	Digest    string
	Agent     string
	ContentID string
	Task      bool
}

// ImageIdentity is the structured identity of an agents-sandbox runner image:
// the project slug, agent and content id that name it. It is the value the
// launcher already holds after rendering a reference, so a caller that built or
// resolved an image never has to parse the reference back into its parts (and
// "which spelling is this" stays a closed union rather than a string-sniffing
// convention). An identity carries a content id only when the image's content
// is known; the name spelling is then derived rather than recorded.
type ImageIdentity struct {
	Slug      string
	Agent     string
	ContentID string
}

// NameRef renders the prefix-free name reference for the identity,
// "<slug>:<agent>-latest", the spelling a 0.6.0 install cached the image under.
// It is empty when the identity is not a complete slug/agent pair: runner
// images are per-agent, so half an identity names no image.
func (i ImageIdentity) NameRef() string {
	if i.Slug == "" || i.Agent == "" {
		return ""
	}
	return RunnerImageRef(i.Slug, i.Agent)
}

// Ref renders the reference the identity names: the content-addressed reference
// when it carries a content id, the name reference otherwise. It is empty for
// an incomplete slug/agent pair.
func (i ImageIdentity) Ref() string {
	if i.ContentID != "" && i.Slug != "" && i.Agent != "" {
		return ContentImageRef(i.Slug, i.Agent, i.ContentID)
	}
	return i.NameRef()
}

// TaskSandboxName returns the prefix-free task sandbox name for a slug, agent
// and creation timestamp in nanoseconds.
func TaskSandboxName(slug, agent string, nanos int64) string {
	return slug + "-" + agent + "-" + strconv.FormatInt(nanos, 10)
}

// HomeVolumeName returns the prefix-free home volume name for a slug, agent and
// timestamp.
func HomeVolumeName(slug, agent, timestamp string) string {
	return slug + "-" + agent + "-" + timestamp
}

// RunnerImageRef returns the prefix-free microsandbox runner image reference
// for a project slug and agent, e.g. "myproject-1mjusbm3wikhb0:opencode-latest".
// It is the builder counterpart to ParseImageTag.
func RunnerImageRef(slug, agent string) string {
	return slug + ":" + agent + "-latest"
}

// ContentImageRef returns the content-addressed microsandbox runner image
// reference for a project slug, agent and content id, e.g.
// "myproject-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31". The content id keys the
// image to its content so a project's variants coexist in the microsandbox
// cache; RunnerImageRef stays the name form. It is the builder counterpart to
// ParseImageTag.
func ContentImageRef(slug, agent, contentID string) string {
	return slug + ":" + agent + "." + contentID
}

// DockerImageRef returns the Docker tag for a project slug and agent. It is also
// the pre-change microsandbox runner reference (the two were one string). It
// keeps the namespace prefix so runner images stay distinguishable in the
// system Docker daemon.
func DockerImageRef(slug, agent string) string {
	return DockerImagePrefix + slug + ":" + agent + "-latest"
}

// findHashSuffix finds the start index of the 14-character base36 project-slug
// hash in a name remainder. It scans right-to-left for the last 14-character
// lowercase-alphanumeric run that forms a whole hyphen-delimited segment
// (preceded by '-', and followed by '-' or the end of the name).
//
// Right-to-left with the segment boundary is what makes the anchor unambiguous.
// The layer name immediately following the hash is an agent and then, for a task
// sandbox, an all-digit nanos run; a folder's own last segment may itself be
// hash-shaped. Anchoring on the *first* run would pick that folder segment, and
// anchoring on the last run without a boundary would pick the nanos run (whose
// first 14 digits are preceded by '-'). The real hash is the rightmost run that
// is bounded by '-' or the end, so it is the only one that satisfies both.
//
// Returns -1 when no such suffix is found.
func findHashSuffix(name string) int {
	for i := len(name) - 14; i >= 1; i-- {
		if name[i-1] != '-' {
			continue
		}
		if i+14 != len(name) && name[i+14] != '-' {
			continue
		}
		ok := true
		for j := range 14 {
			c := name[i+j]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				ok = false
				break // gomutants:disable INVERT_LOOP_CTRL reason="ok is only ever cleared here, never set again, so breaking and continuing both leave it false and the outer check unchanged"
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// ParseImageTag extracts the slug, digest, and agent from a microsandbox runner
// image reference under either scheme. Examples:
//
//	"myproject-1mjusbm3wikhb0:opencode-latest"
//	→ slug="myproject-1mjusbm3wikhb0", agent="opencode"
//
//	"agents-sandbox/runner-myproject:xYz1234AbCdEfGh"
//	→ slug="myproject", digest="xYz1234AbCdEfGh"
//
// A prefix-free reference is recognised by its slug being a single path
// segment, so a registry/repository reference (a foreign image) parses empty.
func ParseImageTag(name string) ArtifactInfo {
	if strings.HasPrefix(name, DockerImagePrefix) {
		return parseImageRemainder(name[len(DockerImagePrefix):], true)
	}
	return parseImageRemainder(name, false)
}

func parseImageRemainder(remainder string, prefixed bool) ArtifactInfo {
	before, after, ok := strings.CutLast(remainder, ":")
	// gomutants:disable-next-line BRANCH_IF reason="without a ':' CutLast returns before==remainder and after=='', so falling through yields the same slug-only (or foreign-zero) result this branch computes"
	if !ok {
		if !prefixed && !looksLikeProjectSlug(remainder) {
			// gomutants:disable-next-line RETURN_ZERO reason="every field is its zero value, so returning a freshly zeroed ArtifactInfo is identical"
			return ArtifactInfo{Slug: "", Digest: "", Agent: "", ContentID: "", Task: false}
		}
		return ArtifactInfo{Slug: remainder, Digest: "", Agent: "", ContentID: "", Task: false}
	}
	tag := after
	slug := before
	if !prefixed && !looksLikeProjectSlug(slug) {
		// gomutants:disable-next-line RETURN_ZERO reason="every field is its zero value, so returning a freshly zeroed ArtifactInfo is identical"
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", ContentID: "", Task: false}
	}
	if agent, ok := strings.CutSuffix(tag, "-latest"); ok {
		return ArtifactInfo{Slug: slug, Agent: agent, Digest: "", ContentID: "", Task: false}
	}
	// contentID is empty when the separator is absent, so the cut's ok result is
	// implied by the non-empty check.
	if agent, contentID, _ := strings.Cut(tag, "."); contentID != "" {
		return ArtifactInfo{Slug: slug, Agent: agent, ContentID: contentID, Digest: "", Task: false}
	}
	if tag != "latest" {
		return ArtifactInfo{Slug: slug, Digest: tag, Agent: "", ContentID: "", Task: false}
	}
	return ArtifactInfo{Slug: slug, Digest: "", Agent: "", ContentID: "", Task: false}
}

// looksLikeProjectSlug reports whether s could be a project slug. A slug is a
// single path segment (no registry/repository slash), which is what separates a
// prefix-free runner ref from a foreign image reference.
func looksLikeProjectSlug(s string) bool {
	return s != "" && !strings.Contains(s, "/")
}

// IsRunnerImageRef reports whether name is an agents-sandbox runner image
// reference under either scheme. Ownership requires the parsed slug to embed the
// 14-character base36 hash a launcher-built project slug ends with, so a
// single-segment reference without that hash — a manually loaded image in the
// private runtime home — is foreign.
func IsRunnerImageRef(name string) bool {
	return slugEmbedsHash(ParseImageTag(name).Slug)
}

// slugEmbedsHash reports whether slug ends in the base36 project-slug hash every
// launcher-built slug carries. A slug without a '-' cuts to an empty hash, which
// isBase36Hash rejects, so the cut's found result is implied.
func slugEmbedsHash(slug string) bool {
	_, hash, _ := strings.CutLast(slug, "-")
	return isBase36Hash(hash)
}

// isBase36Hash reports whether s is a 14-character lowercase-alphanumeric hash
// such as the ones embedded in project slugs.
func isBase36Hash(s string) bool {
	if len(s) != 14 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// allDigits reports whether s is a non-empty run of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ParseVMName extracts the slug, optional agent, and task flag from a sandbox
// name under either scheme. Examples:
//
//	"myproject-1mjusbm3wikhb0-opencode"
//	→ slug="myproject-1mjusbm3wikhb0", agent="opencode"
//
//	"agents-sandbox-vm-projectname-1mjusbm3wikhb0"
//	→ slug="projectname-1mjusbm3wikhb0"
//
//	"myproject-1mjusbm3wikhb0-opencode-1729000000000000000"
//	→ slug="myproject-1mjusbm3wikhb0", agent="opencode", task=true
//
// A name that starts with a legacy prefix may also be a prefix-free sandbox
// whose folder begins with that prefix (a folder literally named
// "agents-sandbox-vm"); the legacy interpretation is used only when it parses,
// otherwise the whole name is parsed prefix-free. Prefer the label-first
// SandboxIdentity for anything the launcher created.
func ParseVMName(name string) ArtifactInfo {
	switch {
	case strings.HasPrefix(name, LegacyTaskPrefix):
		return parseLegacyOrFullSandbox(name, len(LegacyTaskPrefix), true)
	case strings.HasPrefix(name, LegacyVMPrefix):
		return parseLegacyOrFullSandbox(name, len(LegacyVMPrefix), false)
	case strings.HasPrefix(name, LegacyHomePrefix), strings.HasPrefix(name, DockerImagePrefix):
		// A home-volume or runner-image name is not a sandbox — unless it is a
		// hash-anchored prefix-free sandbox whose folder starts with that prefix.
		if info, anchored := parseSandboxBody(name, false); anchored {
			return info
		}
		return ArtifactInfo{}
	}
	info, _ := parseSandboxBody(name, false)
	return info
}

// parseLegacyOrFullSandbox parses a name that starts with a legacy prefix,
// preferring the legacy interpretation (prefix stripped) and using the
// prefix-free one (the whole name) only when the stripped remainder is not a
// hash-anchored sandbox body but the whole name is. A genuine legacy name still
// parses; a prefix-free project VM whose folder begins with the legacy prefix
// (e.g. "agents-sandbox-task-<hash>-opencode") is not misread as a legacy task.
func parseLegacyOrFullSandbox(name string, prefixLen int, task bool) ArtifactInfo {
	remainder := name[prefixLen:]
	if info, anchored := parseSandboxBody(remainder, task); anchored {
		return info
	}
	if info, anchored := parseSandboxBody(name, false); anchored {
		return info
	}
	// Neither is anchored: keep the historical best-effort legacy strip.
	info, _ := parseSandboxBody(remainder, task)
	return info
}

// parseSandboxBody parses a sandbox name body, returning the identity and
// whether it anchored on a slug hash.
func parseSandboxBody(remainder string, task bool) (ArtifactInfo, bool) {
	hashStart := findHashSuffix(remainder)
	if hashStart == -1 {
		return ArtifactInfo{Slug: remainder, Task: task, Digest: "", Agent: "", ContentID: ""}, false
	}
	folderName := remainder[:hashStart-1]
	hash := remainder[hashStart : hashStart+14]
	slug := folderName + "-" + hash
	rest := remainder[hashStart+14:]
	// The hash is hyphen-delimited, so rest is empty or begins with '-'; a bare
	// gomutants:disable-next-line INTEGER_DECREMENT reason="the only one-character rest is \"-\", which falls through to the same slug-only result this early return produces"
	if len(rest) < 2 {
		return ArtifactInfo{Slug: slug, Task: task, Digest: "", Agent: "", ContentID: ""}, true
	}
	rest = rest[1:]
	parts := strings.Split(rest, "-")
	if len(parts) > 1 && allDigits(parts[len(parts)-1]) {
		return ArtifactInfo{
			Slug: slug, Agent: strings.Join(parts[:len(parts)-1], "-"), Task: true, Digest: "", ContentID: "",
		}, true
	}
	return ArtifactInfo{Slug: slug, Agent: rest, Task: task, Digest: "", ContentID: ""}, true
}

// ParseHomeVolumeName extracts the slug, optional digest, and agent from a home
// volume name under either scheme. Examples:
//
//	"myproject-1mjusbm3wikhb0-claude-code-20260812T123456"
//	→ slug="myproject-1mjusbm3wikhb0", agent="claude-code"
//
//	"agents-sandbox-home-myproject-1mjusbm3wikhb0-20260812T123456"
//	→ slug="myproject-1mjusbm3wikhb0"
func ParseHomeVolumeName(name string) ArtifactInfo {
	switch {
	case strings.HasPrefix(name, LegacyHomePrefix):
		return parseLegacyOrFullHome(name, len(LegacyHomePrefix))
	case strings.HasPrefix(name, LegacyVMPrefix), strings.HasPrefix(name, LegacyTaskPrefix),
		strings.HasPrefix(name, DockerImagePrefix):
		// A VM/task/image name is not a home volume — unless it is a
		// hash-anchored prefix-free volume whose folder starts with that prefix.
		if info, anchored := parseHomeVolumeBody(name, false); anchored {
			return info
		}
		// gomutants:disable-next-line RETURN_ZERO reason="every field is its zero value, so returning a freshly zeroed ArtifactInfo is identical"
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", ContentID: "", Task: false}
	default:
		return parseHomeVolumeRemainder(name, false)
	}
}

// parseLegacyOrFullHome mirrors parseLegacyOrFullSandbox for home volumes.
func parseLegacyOrFullHome(name string, prefixLen int) ArtifactInfo {
	remainder := name[prefixLen:]
	if info, anchored := parseHomeVolumeBody(remainder, true); anchored {
		return info
	}
	if info, anchored := parseHomeVolumeBody(name, false); anchored {
		return info
	}
	return parseHomeVolumeRemainder(remainder, true)
}

// parseHomeVolumeBody parses a home-volume body and reports whether it anchored
// on a slug hash.
func parseHomeVolumeBody(remainder string, prefixed bool) (ArtifactInfo, bool) {
	return parseHomeVolumeRemainder(remainder, prefixed), findHashSuffix(remainder) != -1
}

func parseHomeVolumeRemainder(remainder string, prefixed bool) ArtifactInfo {
	// gomutants:disable-next-line BRANCH_IF reason="an empty remainder finds no hash and, prefixed or not, the fall-through returns the same zero ArtifactInfo this branch returns"
	if remainder == "" {
		// gomutants:disable-next-line RETURN_ZERO reason="every field is its zero value, so returning a freshly zeroed ArtifactInfo is identical"
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", ContentID: "", Task: false}
	}
	if hashStart := findHashSuffix(remainder); hashStart != -1 {
		if slug, digest, ok := legacyDigestName(remainder, hashStart); ok {
			return ArtifactInfo{Slug: slug, Digest: digest, Agent: "", ContentID: "", Task: false}
		}
		folderName := remainder[:hashStart-1]
		hash := remainder[hashStart : hashStart+14]
		slug := folderName + "-" + hash
		tail := strings.TrimPrefix(remainder[hashStart+14:], "-")
		parts := strings.Split(tail, "-")
		last := parts[len(parts)-1]
		if isHomeTimestamp(last) {
			return ArtifactInfo{
				Slug: slug, Agent: strings.Join(parts[:len(parts)-1], "-"), Digest: "", ContentID: "", Task: false,
			}
		}
		return ArtifactInfo{Slug: slug, Digest: last, Agent: "", ContentID: "", Task: false}
	}
	// A prefix-free home volume always embeds the project-slug hash; without it
	// the name is not ours.
	if !prefixed {
		// gomutants:disable-next-line RETURN_ZERO reason="every field is its zero value, so returning a freshly zeroed ArtifactInfo is identical"
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", ContentID: "", Task: false}
	}
	// No embedded hash: the positional split still recognises the legacy digest
	// names.
	parts := strings.Split(remainder, "-")
	if len(parts) < 2 {
		return ArtifactInfo{Slug: remainder, Digest: "", Agent: "", ContentID: "", Task: false}
	}
	last := parts[len(parts)-1]
	if isHomeTimestamp(last) {
		agent := ""
		slugParts := parts[:len(parts)-1]
		if len(parts) >= 3 {
			// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="findHashSuffix already rejected every hyphen-delimited 14-char base36 segment, so whichever part this index selects it is never a hash and the branch is taken regardless"
			if !isBase36Hash(parts[len(parts)-2]) {
				agent = parts[len(parts)-2]
				slugParts = parts[:len(parts)-2]
			}
		}
		return ArtifactInfo{Slug: strings.Join(slugParts, "-"), Agent: agent, Digest: "", ContentID: "", Task: false}
	}
	return ArtifactInfo{
		Slug:      strings.Join(parts[:len(parts)-1], "-"),
		Digest:    last,
		Agent:     "",
		ContentID: "",
		Task:      false,
	}
}

// legacyDigestName recognises a legacy digest-named home volume body, whose
// final hash-shaped segment is a content digest and the segment before it is the
// project hash. findHashSuffix anchors on that digest, so the project slug is
// recovered from the segment to its left. Example remainder
// "myproject-1mjusbm3wikhb0-4e8oeqohbmalok" -> slug "myproject-1mjusbm3wikhb0",
// digest "4e8oeqohbmalok".
func legacyDigestName(remainder string, hashStart int) (string, string, bool) {
	if hashStart+14 != len(remainder) {
		return "", "", false
	}
	inner := remainder[:hashStart-1]
	innerHashStart := findHashSuffix(inner)
	if innerHashStart == -1 || innerHashStart+14 != len(inner) {
		return "", "", false
	}
	return inner[:innerHashStart-1] + "-" + inner[innerHashStart:innerHashStart+14], remainder[hashStart : hashStart+14], true
}

// isHomeTimestamp reports whether s looks like a UTC home-volume timestamp
// (YYYYMMDDTHHmmss, 15 chars with 'T' at index 8).
func isHomeTimestamp(s string) bool {
	if len(s) != 15 || s[8] != 'T' || s[0] < '2' || s[0] > '3' {
		return false
	}
	for i, c := range s {
		if i == 8 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

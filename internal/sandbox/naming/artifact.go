package naming

import (
	"strings"
)

// ArtifactInfo is the result of an artifact name parse operation.
type ArtifactInfo struct {
	Slug   string
	Digest string
	Agent  string
}

// findHashSuffix finds the start index of a 14-character base36 hash suffix
// in the name remainder (e.g. "agents-sandbox-1mjusbm3wikhb0" -> returns 6, pointing
// at the '1' in the 14-char hash). Returns -1 when no such suffix is found.
func findHashSuffix(name string) int {
	for i := 1; i < len(name)-13; i++ {
		if name[i-1] != '-' {
			continue
		}
		ok := true
		for j := range 14 {
			c := name[i+j]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// ParseImageTag extracts the slug, digest, and agent from a Docker image
// reference. Examples: "agents-sandbox/runner-myproject:xYz1234AbCdEfGh"
//
//	→ slug="myproject", digest="xYz1234AbCdEfGh", agent=""
//
//	"agents-sandbox/runner-myproject:opencode-latest"
//	→ slug="myproject", digest="", agent="opencode"
//
//	"agents-sandbox/runner-myproject:latest"
//	→ slug="myproject", digest="", agent=""
//
//	"agents-sandbox/runner-myproject"
//	→ slug="myproject", digest="", agent=""
func ParseImageTag(name string) ArtifactInfo {
	if !strings.HasPrefix(name, ImagePrefix) {
		return ArtifactInfo{}
	}
	afterPrefix := name[len(ImagePrefix):]
	lastColon := strings.LastIndex(afterPrefix, ":")
	if lastColon == -1 {
		return ArtifactInfo{Slug: afterPrefix, Digest: "", Agent: ""}
	}
	tag := afterPrefix[lastColon+1:]
	slug := afterPrefix[:lastColon]
	if agent, ok := strings.CutSuffix(tag, "-latest"); ok {
		return ArtifactInfo{Slug: slug, Digest: "", Agent: agent}
	}
	if tag != "" && tag != "latest" {
		return ArtifactInfo{Slug: slug, Digest: tag, Agent: ""}
	}
	return ArtifactInfo{Slug: slug, Digest: "", Agent: ""}
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

// ParseVMName extracts the slug and optional agent from a sandbox name.
// Examples: "agents-sandbox-vm-projectname-1mjusbm3wikhb0"
//
//	→ slug="projectname-1mjusbm3wikhb0", agent=""
//
//	"agents-sandbox-vm-projectname-1mjusbm3wikhb0-opencode"
//	→ slug="projectname-1mjusbm3wikhb0", agent="opencode"
func ParseVMName(name string) ArtifactInfo {
	if !strings.HasPrefix(name, VmPrefix) {
		return ArtifactInfo{}
	}
	remainder := name[len(VmPrefix):]
	hashStart := findHashSuffix(remainder)
	if hashStart == -1 {
		return ArtifactInfo{Slug: remainder, Digest: "", Agent: ""}
	}
	folderName := remainder[:hashStart-1]
	hash := remainder[hashStart : hashStart+14]
	slug := folderName + "-" + hash
	if hashStart+14 < len(remainder) {
		rest := remainder[hashStart+14:]
		if len(rest) > 1 && rest[0] == '-' {
			return ArtifactInfo{Slug: slug, Digest: "", Agent: rest[1:]}
		}
	}
	return ArtifactInfo{Slug: slug, Digest: "", Agent: ""}
}

// ParseHomeVolumeName extracts the slug, optional digest, and agent from a
// home volume name.
// Examples: "agents-sandbox-home-myproject-1mjusbm3wikhb0-20260812T123456"
//
//	→ slug="myproject-1mjusbm3wikhb0", agent=""
func ParseHomeVolumeName(name string) ArtifactInfo {
	if !strings.HasPrefix(name, HomePrefix) {
		return ArtifactInfo{}
	}
	remainder := name[len(HomePrefix):]
	parts := strings.Split(remainder, "-")
	if len(parts) < 2 {
		return ArtifactInfo{Slug: remainder, Digest: "", Agent: ""}
	}
	last := parts[len(parts)-1]
	if isHomeTimestamp(last) {
		// Timestamped format. New: <slug>-<agent>-<ts>; legacy: <slug>-<ts>.
		agent := ""
		slugParts := parts[:len(parts)-1]
		if len(parts) >= 3 && !isBase36Hash(parts[len(parts)-2]) {
			agent = parts[len(parts)-2]
			slugParts = parts[:len(parts)-2]
		}
		return ArtifactInfo{Slug: strings.Join(slugParts, "-"), Digest: "", Agent: agent}
	}
	// Legacy digest format — last part is a 14-char base36 digest hash.
	return ArtifactInfo{
		Slug:   strings.Join(parts[:len(parts)-1], "-"),
		Digest: last,
		Agent:  "",
	}
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

// ArtifactFor dispatches to the appropriate parser based on the name prefix.
func ArtifactFor(name string) ArtifactInfo {
	switch {
	case strings.HasPrefix(name, ImagePrefix):
		return ParseImageTag(name)
	case strings.HasPrefix(name, TaskPrefix):
		remainder := name[len(TaskPrefix):]
		parts := strings.Split(remainder, "-")
		if len(parts) < 2 {
			return ArtifactInfo{Slug: remainder, Digest: "", Agent: ""}
		}
		return ArtifactInfo{Slug: strings.Join(parts[:len(parts)-1], "-"), Digest: "", Agent: ""}
	case strings.HasPrefix(name, VmPrefix):
		return ParseVMName(name)
	case strings.HasPrefix(name, HomePrefix):
		return ParseHomeVolumeName(name)
	}
	return ArtifactInfo{}
}

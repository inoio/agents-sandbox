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
// in the name remainder (e.g. "opencode-sandbox-1mjusbm3wikhb0" -> returns 6, pointing
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
// reference. Examples: "opencode-sandbox/runner-myproject:xYz1234AbCdEfGh"
//
//	→ slug="myproject", digest="xYz1234AbCdEfGh", agent=""
//
//	"opencode-sandbox/runner-myproject:opencode-latest"
//	→ slug="myproject", digest="", agent="opencode"
//
//	"opencode-sandbox/runner-myproject:latest"
//	→ slug="myproject", digest="", agent=""
//
//	"opencode-sandbox/runner-myproject"
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

// ParseVMName extracts the slug and optional branch (digest) from a sandbox name.
// Examples: "opencode-sandbox-vm-projectname-aB3cDe4fGhIjKl"
//
//	→ slug="projectname-aB3cDe4fGhIjKl", digest=""
//
//	"opencode-sandbox-vm-projectname-aB3cDe4fGhIjKl-feature"
//	→ slug="projectname-aB3cDe4fGhIjKl", digest="feature"
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
			return ArtifactInfo{Slug: slug, Digest: rest[1:], Agent: ""}
		}
	}
	return ArtifactInfo{Slug: slug, Digest: "", Agent: ""}
}

// ParseHomeVolumeName extracts the slug and timestamp from a home volume name.
// Examples: "opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-20260812T123456"
//
//	→ slug="myproject-aB3cDe4fGhIjKl", digest="20260812T123456"
func ParseHomeVolumeName(name string) ArtifactInfo {
	if !strings.HasPrefix(name, HomePrefix) {
		return ArtifactInfo{}
	}
	remainder := name[len(HomePrefix):]
	parts := strings.Split(remainder, "-")
	if len(parts) < 2 {
		return ArtifactInfo{Slug: remainder, Digest: "", Agent: ""}
	}
	// Check if last part looks like a timestamp (YYYYMMDDTHHmmss = 15 chars with 'T' at pos 8)
	last := parts[len(parts)-1]
	if len(last) == 15 && last[8] == 'T' && last[0] >= '2' && last[0] <= '3' {
		// Validate all other chars are digits
		valid := true
		for i, c := range last {
			if i == 8 {
				continue
			}
			if c < '0' || c > '9' {
				valid = false
				break
			}
		}
		if valid {
			// Likely a new-format timestamp — treat as slug suffix, not digest
			return ArtifactInfo{Slug: strings.Join(parts[:len(parts)-1], "-"), Digest: "", Agent: ""}
		}
	}
	// Legacy format — last part is a 14-char base36 digest hash
	return ArtifactInfo{
		Slug:   strings.Join(parts[:len(parts)-1], "-"),
		Digest: parts[len(parts)-1],
		Agent:  "",
	}
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

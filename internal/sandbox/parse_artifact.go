package sandbox

import (
	"strings"
)

//nolint:gochecknoglobals // fmt.stringer pattern for StaleType type / consts
var stateName = map[StaleType]string{
	StaleTypeVM:          "vm",
	StaleTypeVolume:      "volume",
	StaleTypeDockerImage: "docker-image",
	StaleTypeMsbImage:    "msb-image",
}

func (ss StaleType) String() string {
	return stateName[ss]
}

// artifactInfo holds the parsed slug and digest from an artifact name.
type artifactInfo struct {
	slug   string
	digest string
}

// findHashSuffix finds the start index of a 14-character base36 hash suffix
// in the name remainder (e.g. "saife-1mjusbm3wikhb0" -> returns 6, pointing
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

// parseImageTag extracts the slug and digest from a Docker image reference.
// Examples: "opencode-msb/runner-myproject:xYz1234AbCdEfGh"
//
//	→ slug="myproject", digest="xYz1234AbCdEfGh"
//
//	"opencode-msb/runner-myproject:latest"
//	→ slug="myproject", digest=""
//
//	"opencode-msb/runner-myproject"
//	→ slug="myproject", digest=""
func parseImageTag(name string) artifactInfo {
	if !strings.HasPrefix(name, imagePrefix) {
		return artifactInfo{}
	}
	afterPrefix := name[len(imagePrefix):]
	lastColon := strings.LastIndex(afterPrefix, ":")
	if lastColon == -1 {
		return artifactInfo{slug: afterPrefix}
	}
	tag := afterPrefix[lastColon+1:]
	slug := afterPrefix[:lastColon]
	if tag != "" && tag != "latest" {
		return artifactInfo{slug: slug, digest: tag}
	}
	return artifactInfo{slug: slug}
}

// parseVMName extracts the slug and optional branch (digest) from a sandbox name.
// Examples: "opencode-msb-vm-projectname-aB3cDe4fGhIjKl"
//
//	→ slug="projectname-aB3cDe4fGhIjKl", digest=""
//
//	"opencode-msb-vm-projectname-aB3cDe4fGhIjKl-feature"
//	→ slug="projectname-aB3cDe4fGhIjKl", digest="feature"
func parseVMName(name string) artifactInfo {
	if !strings.HasPrefix(name, vmPrefix) {
		return artifactInfo{}
	}
	remainder := name[len(vmPrefix):]
	hashStart := findHashSuffix(remainder)
	if hashStart == -1 {
		return artifactInfo{slug: remainder}
	}
	folderName := remainder[:hashStart-1]
	hash := remainder[hashStart : hashStart+14]
	slug := folderName + "-" + hash
	if hashStart+14 < len(remainder) {
		rest := remainder[hashStart+14:]
		if len(rest) > 1 && rest[0] == '-' {
			return artifactInfo{slug: slug, digest: rest[1:]}
		}
	}
	return artifactInfo{slug: slug}
}

// parseHomeVolumeName extracts the slug and timestamp from a home volume name.
// Examples: "opencode-msb-home-myproject-aB3cDe4fGhIjKl-20260812T123456"
//
//	→ slug="myproject-aB3cDe4fGhIjKl", digest="20260812T123456"
func parseHomeVolumeName(name string) artifactInfo {
	if !strings.HasPrefix(name, homePrefix) {
		return artifactInfo{}
	}
	remainder := name[len(homePrefix):]
	parts := strings.Split(remainder, "-")
	if len(parts) < 2 {
		return artifactInfo{slug: remainder}
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
			return artifactInfo{slug: strings.Join(parts[:len(parts)-1], "-")}
		}
	}
	// Legacy format — last part is a 14-char base36 digest hash
	return artifactInfo{
		slug:   strings.Join(parts[:len(parts)-1], "-"),
		digest: parts[len(parts)-1],
	}
}

// parseCloneVolumeName extracts the slug from a clone volume name.
// Clone volumes have no digest component.
func parseCloneVolumeName(name string) string {
	if !strings.HasPrefix(name, clonePrefix) {
		return ""
	}
	remainder := name[len(clonePrefix):]
	parts := strings.Split(remainder, "-")
	if len(parts) < 2 {
		return remainder
	}
	return strings.Join(parts[:len(parts)-1], "-")
}

// extractProjectSlugAndDigest extracts the project slug and optional digest
// from an artifact name (sandbox/volume/Docker image/MSB image).
//
// Examples:
//
//	"opencode-msb-vm-projectname-main" → slug="projectname", digest=""
//	"opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh" → slug="myproject-aB3cDe4fGhIjKl", digest="xYz1234AbCdEfGh"
//	"opencode-msb/runner-myproject:xYz1234AbCdEfGh" → slug="myproject", digest="xYz1234AbCdEfGh"
func extractProjectSlugAndDigest(name string) (string, string) {
	info := artifactFor(name)
	return info.slug, info.digest
}

// artifactFor dispatches to the appropriate parser based on the name prefix.
func artifactFor(name string) artifactInfo {
	switch {
	case strings.HasPrefix(name, imagePrefix):
		return parseImageTag(name)
	case strings.HasPrefix(name, taskPrefix):
		remainder := name[len(taskPrefix):]
		parts := strings.Split(remainder, "-")
		if len(parts) < 2 {
			return artifactInfo{slug: remainder}
		}
		return artifactInfo{slug: strings.Join(parts[:len(parts)-1], "-")}
	case strings.HasPrefix(name, vmPrefix):
		return parseVMName(name)
	case strings.HasPrefix(name, homePrefix):
		return parseHomeVolumeName(name)
	case strings.HasPrefix(name, clonePrefix):
		return artifactInfo{slug: parseCloneVolumeName(name)}
	}
	return artifactInfo{}
}

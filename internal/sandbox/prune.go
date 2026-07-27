package sandbox

import (
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// StaleReport describes the result of a prune operation.
type StaleReport struct {
	PrunedVMs           int
	PrunedVolumes       int
	PrunedDockerImages  int
	PrunedMSBImages     int
	PrunedTaskSandboxes int
	PrunedCloneVolumes  int
	Details             []StaleEntry
}

// StaleEntry describes a single artifact that was pruned or would be pruned.
type StaleEntry struct {
	Type     string        // "vm", "volume", "docker-image", "msb-image", "task-sandbox", "clone-volume"
	Name     string
	StaleFor time.Duration
}

// staleVM is an internal type used by findStaleVMs.
type staleVM struct {
	name      string
	status    msb.SandboxStatus
	updatedAt time.Time
}

// extractProjectSlugAndDigest extracts the project slug and optional digest
// from an artifact name (sandbox/volume/Docker image/MSB image).
//
// Examples:
//
//	"opencode-msb-vm-projectname-main" → slug="projectname", digest=""
//	"opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh" → slug="myproject-aB3cDe4fGhIjKl", digest="xYz1234AbCdEfGh"
//	"opencode-msb/runner-myproject:xYz1234AbCdEfGh" → slug="myproject", digest="xYz1234AbCdEfGh"
func extractProjectSlugAndDigest(name string) (slug, digest string) {
	// Handle image references: opencode-msb/runner-{slug}:{tag}
	if strings.HasPrefix(name, "opencode-msb/runner-") {
		afterPrefix := name[len("opencode-msb/runner-"):]
		lastColon := strings.LastIndex(afterPrefix, ":")
		if lastColon == -1 {
			return afterPrefix, ""
		}
		tag := afterPrefix[lastColon+1:]
		slug = afterPrefix[:lastColon]
		if tag != "" && tag != "latest" {
			digest = tag
		}
		return slug, digest
	}

	// For sandbox and volume names, strip prefix and parse remainder.
	var prefixLen int
	var kind string
	switch {
	case strings.HasPrefix(name, "opencode-msb-vm-"):
		prefixLen = len("opencode-msb-vm-")
		kind = "vm"
	case strings.HasPrefix(name, "opencode-msb-home-"):
		prefixLen = len("opencode-msb-home-")
		kind = "home"
	case strings.HasPrefix(name, "opencode-msb-clone-"):
		prefixLen = len("opencode-msb-clone-")
		kind = "clone"
	case strings.HasPrefix(name, "opencode-msb-task-"):
		prefixLen = len("opencode-msb-task-")
		kind = "task"
	default:
		return "", ""
	}

	remainder := name[prefixLen:]
	parts := strings.Split(remainder, "-")

	if len(parts) < 2 {
		return remainder, ""
	}

	switch kind {
	case "vm":
		// VM: "slug-branch" → slug is everything before last dash.
		return strings.Join(parts[:len(parts)-1], "-"), ""
	case "home":
		// Home volume: "slug-digest" → digest is last part, rest is slug.
		digest = parts[len(parts)-1]
		slug = strings.Join(parts[:len(parts)-1], "-")
		return slug, digest
	default:
		// Clone volumes and task sandboxes: no digest, just slug.
		return strings.Join(parts[:len(parts)-1], "-"), ""
	}
}

// isStoppedStatus returns true if the status indicates the sandbox is not
// actively running (stopped or crashed).
func isStoppedStatus(status msb.SandboxStatus) bool {
	return status == msb.SandboxStatusStopped || status == msb.SandboxStatusCrashed
}

// findStaleVMs filters sandboxes to only those that are stopped/crashed and
// older than the given threshold.
func findStaleVMs(sandboxes []staleVM, threshold time.Duration) []StaleEntry {
	var stale []StaleEntry
	for _, s := range sandboxes {
		if !isStoppedStatus(s.status) {
			continue
		}
		elapsed := time.Since(s.updatedAt)
		if elapsed > threshold {
			stale = append(stale, StaleEntry{
				Type:     "vm",
				Name:     s.name,
				StaleFor: elapsed,
			})
		}
	}
	return stale
}



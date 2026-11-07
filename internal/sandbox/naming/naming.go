package naming

import "strings"

// Prefix is the canonical base name for all agents-sandbox naming
// conventions.
const Prefix = "agents-sandbox"

// DockerImagePrefix namespaces the runner images in the system Docker daemon,
// keeping them distinguishable from user images. The Docker tag is unchanged by
// the prefix-free runtime naming.
const DockerImagePrefix = Prefix + "/runner-"

// Legacy prefixes of the microsandbox artifacts created before the naming
// change. Lookups accept both shapes; the prefixes are an ownership marker only
// for the foreign ~/.microsandbox scan, which a user's own microsandbox shared.
const (
	LegacyVMPrefix   = Prefix + "-vm-"
	LegacyHomePrefix = Prefix + "-home-"
	LegacyTaskPrefix = Prefix + "-task-"
)

// IsLegacyProjectSandbox reports whether name is a prefixed project VM name.
func IsLegacyProjectSandbox(name string) bool {
	return strings.HasPrefix(name, LegacyVMPrefix)
}

// IsLegacyTaskSandbox reports whether name is a prefixed task worker name.
func IsLegacyTaskSandbox(name string) bool {
	return strings.HasPrefix(name, LegacyTaskPrefix)
}

// IsLegacyLauncherSandbox reports whether name is a prefixed sandbox
// agents-sandbox manages: a project VM or a task worker.
func IsLegacyLauncherSandbox(name string) bool {
	return IsLegacyProjectSandbox(name) || IsLegacyTaskSandbox(name)
}

// IsLegacyHomeVolume reports whether name is a prefixed home volume name.
func IsLegacyHomeVolume(name string) bool {
	return strings.HasPrefix(name, LegacyHomePrefix)
}

// IsLauncherSandbox reports whether a sandbox belongs to agents-sandbox. It is
// true when the labels carry a launcher slug, when the name is a legacy-prefixed
// launcher sandbox, or when the name parses to a hash-anchored project slug. A
// foreign sandbox created directly with msb — a name without the launcher's
// 14-character slug hash — is not owned.
func IsLauncherSandbox(name string, labels map[string]string) bool {
	if labels[LabelSlug] != "" {
		return true
	}
	if IsLegacyLauncherSandbox(name) {
		return true
	}
	return slugEmbedsHash(SandboxIdentity(name, labels).Slug)
}

// SandboxIdentity resolves a sandbox's project key and kind. Labels the launcher
// writes are authoritative; a sandbox created before they existed falls back to
// the name heuristic (ParseVMName plus the legacy task marker). A nil labels map
// is the pure name fallback.
func SandboxIdentity(name string, labels map[string]string) ArtifactInfo {
	info := ParseVMName(name)
	if labels[LabelSlug] != "" {
		info.Slug = labels[LabelSlug]
		info.Agent = labels[LabelAgent]
		info.Task = labels[LabelType] == SandboxTypeTask
		return info
	}
	if labels[LabelType] == SandboxTypeTask || labels[LabelTask] != "" {
		info.Task = true
	}
	return info
}

package naming

// Test-only name fixtures. The production constructors live where truncation
// and persisted state are known (vm.projectVMName, volume.HomeVolumeName);
// these build a scheme-shaped name from a bare slug so the parser round-trip
// and fuzz tests can generate inputs under either scheme.

func projectVMName(slug, agent string) string {
	return slug + "-" + agent
}

func legacyProjectVMName(slug, agent string) string {
	return LegacyVMPrefix + slug + "-" + agent
}

func legacyHomeVolumeName(slug, agent, timestamp string) string {
	return LegacyHomePrefix + slug + "-" + agent + "-" + timestamp
}

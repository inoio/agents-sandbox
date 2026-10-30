package state

// RecordAgentVersion records the agent release baked into the project's runner
// image as its upgrade baseline. It is a no-op for an empty version (a
// non-release sentinel image) and when no state file exists, so it never creates
// a fingerprint-less partial state before home resolution (#183). Routing it
// through WithHomeState preserves the fields the caller does not own.
func RecordAgentVersion(k Key, version string) error {
	if version == "" {
		return nil
	}
	return WithHomeState(k, func(hs *HomeState, exists bool) (bool, error) {
		if !exists || hs.AgentVersion == version {
			return false, nil
		}
		hs.AgentVersion = version
		return true, nil
	})
}

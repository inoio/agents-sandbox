package viperconfig

// EnvVarNames returns the environment variable names bound to key: the
// canonical AGENTS_SANDBOX_<KEY> name first, then its deprecated
// OPENCODE_SANDBOX_<KEY> alias. A caller testing whether the environment
// supplies a key checks these in order, since the canonical name wins.
func EnvVarNames(key string) []string {
	return []string{envVarName(EnvPrefix, key), envVarName(LegacyEnvPrefix, key)}
}

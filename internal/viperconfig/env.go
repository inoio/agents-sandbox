package viperconfig

import (
	"os"
	"slices"
	"strings"
)

const (
	// EnvPrefix is the primary launcher environment-variable prefix.
	EnvPrefix = "AGENTS_SANDBOX"
	// LegacyEnvPrefix is the deprecated prefix kept as an alias during the
	// rename. It resolves with lower precedence than EnvPrefix.
	LegacyEnvPrefix = "OPENCODE_SANDBOX"
)

// envKeyReplacer maps config-key separators to the underscore used in env
// var names.
var envKeyReplacer = strings.NewReplacer("-", "_", ".", "_")

// envVarName returns the environment variable name for key under prefix.
func envVarName(prefix, key string) string {
	return strings.ToUpper(prefix + "_" + envKeyReplacer.Replace(key))
}

// legacyEnvVarsInUse reports which deprecated-prefix variables are in effect.
// A legacy variable is ignored when its AGENTS_SANDBOX_ counterpart is set.
func legacyEnvVarsInUse() []string {
	keys := slices.Concat(configEnvKeys, []string{"notify"})
	var inUse []string
	for _, key := range keys {
		legacy := envVarName(LegacyEnvPrefix, key)
		if os.Getenv(legacy) == "" {
			continue
		}
		if os.Getenv(envVarName(EnvPrefix, key)) != "" {
			continue
		}
		inUse = append(inUse, legacy)
	}
	return inUse
}

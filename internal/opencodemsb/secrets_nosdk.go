//go:build !cgo

package opencodemsb

import "os"

// SecretEntry mirrors microsandbox.SecretEntry for environments without CGO.
type SecretEntry struct {
	EnvVar            string
	Value             string
	AllowHosts        []string
	AllowHostPatterns []string
	Placeholder       string
	RequireTLS        *bool
}

// SecretEnvOptions mirrors microsandbox.SecretEnvOptions.
type SecretEnvOptions struct {
	AllowHosts        []string
	AllowHostPatterns []string
	Placeholder       string
	RequireTLS        *bool
}

// secretFactory mirrors microsandbox.secretFactory.
type secretFactory struct{}

// Secret mirrors microsandbox.Secret.
var Secret secretFactory

// Env creates a SecretEntry (mirrors microsandbox.Secret.Env).
func (secretFactory) Env(envVar, value string, opts SecretEnvOptions) SecretEntry {
	return SecretEntry{
		EnvVar:            envVar,
		Value:             value,
		AllowHosts:        opts.AllowHosts,
		AllowHostPatterns: opts.AllowHostPatterns,
		Placeholder:       opts.Placeholder,
		RequireTLS:        opts.RequireTLS,
	}
}

// BuildSecrets builds a slice of SecretEntry values from environment variables.
// This is the non-CGO implementation that uses local types.
func BuildSecrets() []SecretEntry {
	var secrets []SecretEntry
	for envVar, host := range SecretMap {
		value := os.Getenv(envVar)
		if value == "" {
			warn(envVar + " not set; related provider/API may fail.")
			continue
		}
		secrets = append(secrets, Secret.Env(
			envVar,
			value,
			SecretEnvOptions{AllowHosts: []string{host}},
		))
	}
	return secrets
}

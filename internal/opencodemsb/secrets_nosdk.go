//go:build !cgo

package opencodemsb

import "os"

type SecretEntry struct {
	EnvVar            string
	Value             string
	AllowHosts        []string
	AllowHostPatterns []string
	Placeholder       string
	RequireTLS        *bool
}

type SecretEnvOptions struct {
	AllowHosts        []string
	AllowHostPatterns []string
	Placeholder       string
	RequireTLS        *bool
}

type secretFactory struct{}

var Secret secretFactory

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

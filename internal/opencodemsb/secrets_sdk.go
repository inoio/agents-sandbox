//go:build cgo

package opencodemsb

import (
	m "github.com/superradcompany/microsandbox/sdk/go"
)

// BuildSecrets builds a slice of SecretEntry values from environment variables.
// It reads each env var listed in SecretMap and creates a SecretEntry if set.
// If an env var is not set, it logs a warning and skips it.
func BuildSecrets() []m.SecretEntry {
	var secrets []m.SecretEntry
	for envVar, host := range SecretMap {
		value := os.Getenv(envVar)
		if value == "" {
			warn(envVar + " not set; related provider/API may fail.")
			continue
		}
		secrets = append(secrets, m.Secret.Env(
			envVar,
			value,
			m.SecretEnvOptions{AllowHosts: []string{host}},
		))
	}
	return secrets
}

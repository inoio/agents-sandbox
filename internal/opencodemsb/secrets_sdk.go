//go:build cgo

package opencodemsb

import (
	"os"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

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

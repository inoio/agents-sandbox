package sandbox

import (
	"os"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const litellmHost = "litellm.inoio.de"

var secretMap = map[string]string{ //nolint:gochecknoglobals // static env-to-host mapping, never mutated
	"LITELLM_API_KEY": litellmHost,
}

func BuildSecrets(logger *log.Logger) []msb.SecretEntry {
	var secrets []msb.SecretEntry
	for envVar, host := range secretMap {
		value := os.Getenv(envVar)
		if value == "" {
			logger.Warn(envVar + " not set; related provider/API may fail.")
			continue
		}
		secrets = append(secrets, msb.Secret.Env(
			envVar,
			value,
			msb.SecretEnvOptions{AllowHosts: []string{host}},
		))
	}
	return secrets
}

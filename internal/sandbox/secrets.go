package sandbox

import (
	"os"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

var secretMap = map[string]string{
	"LITELLM_API_KEY": "litellm.inoio.de",
	"GITHUB_TOKEN":    "github.com",
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

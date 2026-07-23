package sandbox

import (
	"fmt"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func BuildSecrets(secretLines map[string]string, logger *log.Logger) []msb.SecretEntry {
	var secrets []msb.SecretEntry
	for envVar, valueAndHost := range secretLines {
		parts := strings.SplitN(valueAndHost, "@", envKeyValueParts)
		if len(parts) == envKeyValueParts {
			value := parts[0]
			host := parts[1]
			secrets = append(secrets, msb.Secret.Env(
				envVar,
				value,
				msb.SecretEnvOptions{AllowHosts: []string{host}},
			))
		} else {
			logger.Warn(fmt.Sprint(
				"Value of secret '", envVar, "' not defined in format 'value@host': '", valueAndHost, "'",
			))
		}
	}
	return secrets
}

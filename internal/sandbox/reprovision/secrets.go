package reprovision

import (
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func buildSecrets(secretLines map[string]string, ui termio.UI) []msb.SecretEntry {
	var secrets []msb.SecretEntry
	for envVar, valueAndHost := range secretLines {
		parts := strings.SplitN(valueAndHost, "@", options.EnvKeyValueParts)
		if len(parts) == options.EnvKeyValueParts {
			value := parts[0]
			host := parts[1]
			secrets = append(secrets, msb.Secret.Env(
				envVar,
				value,
				msb.SecretEnvOptions{AllowHosts: []string{host}},
			))
		} else {
			ui.Warnf("Value of secret '%s' not defined in format 'value@host': '%s'", envVar, valueAndHost)
		}
	}
	return secrets
}

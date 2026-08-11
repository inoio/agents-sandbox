package sandbox

import (
	"maps"
	"os"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msb "github.com/superradcompany/microsandbox/sdk/go"
	"gopkg.in/yaml.v3"
)

type secretSpec struct {
	Value string   `yaml:"value"`
	Host  string   `yaml:"host"`
	Hosts []string `yaml:"hosts"`
}

const defaultSecretHost = "microsandbox"

func buildSecretsFromSpecs(specs map[string]secretSpec, ui termio.UI) []msb.SecretEntry {
	var secrets []msb.SecretEntry
	for envVar, spec := range specs {
		if spec.Value == "" {
			ui.Warnf("Value of secret '%s' is empty; dropping", envVar)
			continue
		}
		hosts := append([]string{}, spec.Hosts...)
		if spec.Host != "" {
			hosts = append(hosts, spec.Host)
		}
		if len(hosts) == 0 {
			hosts = []string{defaultSecretHost}
		}
		secrets = append(secrets, msb.Secret.Env(
			envVar,
			spec.Value,
			msb.SecretEnvOptions{AllowHosts: hosts},
		))
	}
	return secrets
}

func parseSecretSpecLegacy(filename string, ui termio.UI) map[string]secretSpec {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	specs := make(map[string]secretSpec)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eqParts := strings.SplitN(line, "=", envKeyValueParts)
		if len(eqParts) != envKeyValueParts {
			continue
		}
		key := strings.TrimSpace(eqParts[0])
		if key == "" {
			continue
		}
		valueAndHost := eqParts[1]
		parts := strings.SplitN(valueAndHost, "@", envKeyValueParts)
		if len(parts) != envKeyValueParts {
			ui.Warnf("Value of secret '%s' not defined in format 'value@host': '%s'", key, valueAndHost)
			continue
		}
		specs[key] = secretSpec{Value: parts[0], Host: "", Hosts: []string{parts[1]}}
	}
	return specs
}

func parseSecretSpecYAML(filename string, ui termio.UI) map[string]secretSpec {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	var specs map[string]secretSpec
	if err := yaml.Unmarshal(data, &specs); err != nil {
		ui.Warnf("Parsing secret file '%s' as YAML: %v", filename, err)
		return nil
	}
	return specs
}

func buildSecrets(secretLines map[string]string, ui termio.UI) []msb.SecretEntry {
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
			ui.Warnf("Value of secret '%s' not defined in format 'value@host': '%s'", envVar, valueAndHost)
		}
	}
	return secrets
}

func mergeSecretSpecs(mapsToMerge ...map[string]secretSpec) map[string]secretSpec {
	var result map[string]secretSpec
	for _, m := range mapsToMerge {
		if result == nil {
			result = m
			continue
		}
		maps.Copy(result, m)
	}
	return result
}

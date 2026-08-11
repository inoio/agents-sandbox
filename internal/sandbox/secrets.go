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
	Value                 string   `yaml:"value"`
	Host                  string   `yaml:"host"`
	Hosts                 []string `yaml:"hosts"`
	AllowAnyHostDangerous bool     `yaml:"allow_any_host_dangerous"`
}

func buildSecretsFromSpecs(specs map[string]secretSpec, ui termio.UI) []msb.SecretEntry {
	var secrets []msb.SecretEntry
	for envVar, spec := range specs {
		if spec.Host == "" && len(spec.Hosts) == 0 && !spec.AllowAnyHostDangerous {
			ui.Warnf("Secret '%s' has no hosts or allow_any_host_dangerous; dropping", envVar)
			continue
		}
		hosts := append([]string{}, spec.Hosts...)
		if spec.Host != "" {
			hosts = append(hosts, spec.Host)
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
		atIdx := strings.LastIndex(valueAndHost, "@")
		if atIdx < 0 {
			ui.Warnf("Value of secret '%s' not defined in format 'value@host': '%s'", key, valueAndHost)
			continue
		}
		specs[key] = secretSpec{ //nolint:exhaustruct // missing field uses zero-value default
			Value: valueAndHost[:atIdx], Host: "", Hosts: []string{valueAndHost[atIdx+1:]},
		}
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

package reprovision

import (
	"maps"
	"os"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msb "github.com/superradcompany/microsandbox/sdk/go"
	"gopkg.in/yaml.v3"
)

type SecretSpec struct {
	Value                 string   `yaml:"value"`
	Host                  string   `yaml:"host"`
	Hosts                 []string `yaml:"hosts"`
	AllowAnyHostDangerous bool     `yaml:"allow_any_host_dangerous"`
}

func BuildSecretsFromSpecs(specs map[string]SecretSpec, ui termio.UI) []msb.SecretEntry {
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

func ParseSecretSpecLegacy(filename string, ui termio.UI) map[string]SecretSpec {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	specs := make(map[string]SecretSpec)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eqParts := strings.SplitN(line, "=", options.EnvKeyValueParts)
		if len(eqParts) != options.EnvKeyValueParts {
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
		specs[key] = SecretSpec{ //nolint:exhaustruct // missing field uses zero-value default
			Value: valueAndHost[:atIdx], Host: "", Hosts: []string{valueAndHost[atIdx+1:]},
		}
	}
	return specs
}

func ParseSecretSpecYAML(filename string, ui termio.UI) map[string]SecretSpec {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	var specs map[string]SecretSpec
	if err := yaml.Unmarshal(data, &specs); err != nil {
		ui.Warnf("Parsing secret file '%s' as YAML: %v", filename, err)
		return nil
	}
	return specs
}

func MergeSecretSpecs(mapsToMerge ...map[string]SecretSpec) map[string]SecretSpec {
	var result map[string]SecretSpec
	for _, m := range mapsToMerge {
		if result == nil {
			result = m
			continue
		}
		maps.Copy(result, m)
	}
	return result
}

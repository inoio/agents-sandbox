package reprovision

import (
	"maps"
	"os"
	"strings"

	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
	"gopkg.in/yaml.v3"
)

type SecretSpec struct {
	Value                 string   `yaml:"value"`
	Host                  string   `yaml:"host"`
	Hosts                 []string `yaml:"hosts"`
	AllowAnyHostDangerous bool     `yaml:"allow_any_host_dangerous"`
}

func BuildSecretsFromSpecs(specs map[string]SecretSpec, ui termio.UI) []msbSdk.SecretEntry {
	var secrets []msbSdk.SecretEntry
	for envVar, spec := range specs {
		if spec.Host == "" && len(spec.Hosts) == 0 && !spec.AllowAnyHostDangerous {
			ui.Warnf("Secret '%s' has no hosts or allow_any_host_dangerous; dropping", envVar)
			continue
		}
		hosts := append([]string{}, spec.Hosts...)
		if spec.Host != "" {
			hosts = append(hosts, spec.Host)
		}
		secrets = append(secrets, msbSdk.Secret.Env(
			envVar,
			spec.Value,
			msbSdk.SecretEnvOptions{AllowHosts: hosts},
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
	_ = parseKeyValueLines(string(data), func(key, value string) error {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil
		}
		valueAndHost := value
		atIdx := strings.LastIndex(valueAndHost, "@")
		if atIdx < 0 {
			ui.Warnf("Value of secret '%s' not defined in format 'value@host': '%s'", key, valueAndHost)
			return nil
		}
		specs[key] = SecretSpec{ //nolint:exhaustruct // missing field uses zero-value default
			Value: valueAndHost[:atIdx], Host: "", Hosts: []string{valueAndHost[atIdx+1:]},
		}
		return nil
	})
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

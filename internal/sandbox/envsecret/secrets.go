package envsecret

import (
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
	"go.yaml.in/yaml/v3"
)

// SecretSpec describes a secret to provision into the VM.
//
//exhaustruct:ignore
type SecretSpec struct {
	Value                 string   `yaml:"value"`
	Host                  string   `yaml:"host"`
	Hosts                 []string `yaml:"hosts"`
	AllowAnyHostDangerous bool     `yaml:"allow_any_host_dangerous"`
}

func BuildSecretsFromSpecs(specs map[string]SecretSpec, ui termio.UI) []msbSdk.SecretEntry {
	envVars := make([]string, 0, len(specs))
	for envVar := range specs {
		envVars = append(envVars, envVar)
	}
	sort.Strings(envVars)
	var secrets []msbSdk.SecretEntry
	for _, envVar := range envVars {
		spec := specs[envVar]
		if spec.Host == "" && len(spec.Hosts) == 0 && !spec.AllowAnyHostDangerous {
			ui.Warnf("Secret '%s' has no hosts or allow_any_host_dangerous; dropping", envVar)
			continue
		}
		hosts := append([]string{}, spec.Hosts...)
		if spec.Host != "" {
			hosts = append(hosts, spec.Host)
		}
		if len(hosts) == 0 {
			hosts = []string{"*"}
		}
		secrets = append(secrets, msbSdk.Secret.Env(
			envVar,
			spec.Value,
			msbSdk.SecretEnvOptions{Allow: hosts},
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
		before, after, ok := strings.CutLast(valueAndHost, "@")
		if !ok {
			ui.Warnf("Value of secret '%s' not defined in format 'value@host': '%s'", key, valueAndHost)
			return nil
		}
		specs[key] = SecretSpec{
			Value: before, Host: "", Hosts: []string{after},
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
	specs := make(map[string]SecretSpec)
	if err := yaml.Unmarshal(data, &specs); err != nil {
		ui.Warnf("Parsing secret file '%s' as YAML: %v", filename, err)
		return nil
	}
	return specs
}

// MergeSecretSpecs merges multiple secret-spec maps into a fresh map. Later
// maps win on key collisions, and no argument map is modified.
func MergeSecretSpecs(mapsToMerge ...map[string]SecretSpec) map[string]SecretSpec {
	result := make(map[string]SecretSpec)
	for _, m := range mapsToMerge {
		maps.Copy(result, m)
	}
	return result
}

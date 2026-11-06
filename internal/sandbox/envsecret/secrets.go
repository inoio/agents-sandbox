package envsecret

import (
	"errors"
	"maps"
	"slices"
	"sort"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// SecretSpec describes a secret to provision into the VM.
//
//exhaustruct:ignore
type SecretSpec struct {
	Value                 string   `yaml:"value"`
	Host                  string   `yaml:"host"`
	Hosts                 []string `yaml:"hosts"`
	HeaderFields          []string `yaml:"header_fields"`
	AllowAnyHostDangerous bool     `yaml:"allow_any_host_dangerous"`
}

func BuildSecretsFromSpecs(
	specs map[string]SecretSpec,
	allowPlaceholderFor []string,
	ui termio.Logger,
) []msbSdk.SecretEntry {
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
			msbSdk.SecretEnvOptions{
				Allow:               hosts,
				AllowPlaceholderFor: placeholderHostsFor(hosts, allowPlaceholderFor),
				Substitution: msbSdk.SecretSubstitution{
					HeaderFields: normalizeHeaderFields(spec.HeaderFields),
				},
			},
		))
	}
	return secrets
}

// normalizeHeaderFields lowercases, deduplicates and sorts header field names
// so case- or order-only edits compare equal, and returns nil when nothing
// remains (an empty list means every header may be substituted).
func normalizeHeaderFields(fields []string) []string {
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fields))
	normalized := make([]string, 0, len(fields))
	for _, field := range fields {
		lower := strings.ToLower(field)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		normalized = append(normalized, lower)
	}
	sort.Strings(normalized)
	return normalized
}

// placeholderHostsFor returns the hosts from allowPlaceholderFor that may
// receive spec's unchanged placeholder: the launcher-wide list minus the hosts
// the secret already allows, deduplicated and sorted. A host cannot be both
// substituted for and placeholder-passthrough, so the runtime rejects an entry
// that lists one host in both; excluding an allowed host keeps substitution
// (Allow wins). A secret that allows every host already substitutes
// everywhere, so it gets no placeholder passthrough at all. The result is nil
// when nothing remains, so an unset allow-placeholder-for does not change the
// built entry.
func placeholderHostsFor(allowHosts, allowPlaceholderFor []string) []string {
	if slices.Contains(allowHosts, "*") {
		return nil
	}
	var placeholderHosts []string
	for _, host := range allowPlaceholderFor {
		if slices.Contains(allowHosts, host) || slices.Contains(placeholderHosts, host) {
			continue
		}
		placeholderHosts = append(placeholderHosts, host)
	}
	sort.Strings(placeholderHosts)
	return placeholderHosts
}

func ParseSecretSpecLegacy(filename string, ui termio.Logger) map[string]SecretSpec {
	data, err := configread.ReadFile(filename)
	if err != nil {
		if errors.Is(err, configread.ErrTooLarge) {
			ui.Warnf("ignoring secret file '%s': %v", filename, err)
		}
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

func ParseSecretSpecYAML(filename string, ui termio.Logger) map[string]SecretSpec {
	data, err := configread.ReadFile(filename)
	if err != nil {
		if errors.Is(err, configread.ErrTooLarge) {
			ui.Warnf("ignoring secret file '%s': %v", filename, err)
		}
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

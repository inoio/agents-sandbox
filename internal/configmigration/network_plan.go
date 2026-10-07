package configmigration

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

func migrationHomeMapping(root map[string]any, target string) (string, bool) {
	home, ok := root[homeConfigKey].(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := home[target]
	if !ok {
		return "", false
	}
	switch entry := value.(type) {
	case string:
		return entry, true
	case map[string]any:
		source, _ := entry["source"].(string)
		return source, true
	default:
		return "", true
	}
}

func buildLauncherConfigForPlan(
	a agent.Agent,
	spec agent.ConfigMigrationSpec,
	needsCredentialMapping bool,
) (string, []byte, error) {
	if needsCredentialMapping {
		path, data, err := buildLauncherConfigFn(a, spec)
		if err != nil || data != nil {
			return path, data, err
		}
		data, err = os.ReadFile(path)
		if err != nil {
			return "", nil, fmt.Errorf("read launcher config %s: %w", path, err)
		}
		return path, data, nil
	}
	path, data, err := migrationLoadLauncherConfig()
	if err != nil {
		return "", nil, err
	}
	return path, data, nil
}

func updateLauncherNetwork(
	path string,
	data []byte,
	hosts []string,
	deny []string,
) (string, []byte, []string, []string, error) {
	if len(hosts) == 0 {
		return path, data, nil, nil, nil
	}
	config, err := parseLauncherConfig(path, data)
	if err != nil {
		return "", nil, nil, nil, err
	}
	networkConfig, _ := config.values["network"].(map[string]any)
	allow := stringList(networkConfig["egress-allow"])
	deny = appendUniqueHosts(deny, stringList(networkConfig["egress-deny"])...)
	var added, skipped []string
	for _, host := range hosts {
		if hostDeniedByAny(deny, host) {
			skipped = append(skipped, host)
			continue
		}
		if !slices.Contains(allow, host) && !slices.Contains(added, host) {
			added = append(added, host)
		}
	}
	if len(added) == 0 {
		return path, data, nil, skipped, nil
	}
	config.appendListItems([]string{"network", "egress-allow"}, added)
	updated, err := config.marshal()
	if err != nil {
		return "", nil, nil, nil, err
	}
	return path, updated, added, skipped, nil
}

func hostDeniedByAny(deny []string, host string) bool {
	return slices.ContainsFunc(deny, func(rule string) bool {
		return network.MatchesDestination(rule, host)
	})
}

func planNetworkDeny(plan *Plan) []string {
	return appendUniqueHosts(append([]string(nil), plan.policy.EgressDeny...), projectNetworkDenyHosts()...)
}

func hostsExcluding(hosts, excluded []string) []string {
	var survivors []string
	for _, host := range hosts {
		if !slices.Contains(excluded, host) {
			survivors = append(survivors, host)
		}
	}
	return survivors
}

func plannedNetworkHosts(secrets map[string]Secret) []string {
	seen := make(map[string]struct{})
	for _, secret := range secrets {
		for _, host := range append(append([]string{}, secret.Hosts...), secret.Host) {
			if host != "" {
				seen[host] = struct{}{}
			}
		}
	}
	hosts := make([]string, 0, len(seen))
	for host := range seen {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func projectNetworkDenyHosts() []string {
	path, data, ok := migrationFindLauncherConfig(cp.Get().ProjectConfigDir())
	if !ok {
		return nil
	}
	var root map[string]any
	if migrationIsJSONConfig(path) {
		if json5.Unmarshal(data, &root) != nil {
			return nil
		}
	} else if yaml.Unmarshal(data, &root) != nil {
		return nil
	}
	networkConfig, ok := root["network"].(map[string]any)
	if !ok {
		return nil
	}
	return stringList(networkConfig["egress-deny"])
}

func migrationFindLauncherConfig(dir string) (string, []byte, bool) {
	for _, ext := range []string{extYAML, extYML, extJSON, extJSONC, extJSON5} {
		path := filepath.Join(dir, "config"+ext)
		data, err := os.ReadFile(path)
		if err == nil {
			return path, data, true
		}
	}
	return "", nil, false
}

// stringList reads a config list given as a sequence or a comma-separated
// string, trimming each entry and dropping empty ones.
func stringList(value any) []string {
	var entries []string
	switch values := value.(type) {
	case []string:
		entries = values
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				entries = append(entries, text)
			}
		}
	case string:
		entries = strings.Split(values, ",")
	}
	var result []string
	for _, entry := range entries {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func appendUniqueHosts(hosts []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(hosts)+len(additions))
	result := make([]string, 0, len(hosts)+len(additions))
	for _, host := range append(hosts, additions...) {
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		result = append(result, host)
	}
	sort.Strings(result)
	return result
}

func normalizeClaudeEndpoint(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = claudeAnthropicAPIHost
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", errors.New("claude API endpoint must be a host or an http(s) URL")
	}
	if err := rejectEmbeddedEndpointCredential(value); err != nil {
		return "", "", fmt.Errorf("claude API endpoint: %w", err)
	}
	return parsed.Hostname(), parsed.String(), nil
}

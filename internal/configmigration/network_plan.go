package configmigration

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/titanous/json5"
	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configfile"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

const (
	launcherNetworkSection = "network"
	launcherEgressDenyKey  = "egress-deny"
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
	config, err := configfile.Parse(path, data)
	if err != nil {
		return "", nil, nil, nil, err
	}
	networkConfig, _ := config.Values()[launcherNetworkSection].(map[string]any)
	allow := stringList(networkConfig["egress-allow"])
	deny = appendUniqueHosts(deny, stringList(networkConfig[launcherEgressDenyKey])...)
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
	config.AppendListItems([]string{launcherNetworkSection, "egress-allow"}, added)
	updated, err := config.Marshal()
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
	deny := appendUniqueHosts(append([]string(nil), plan.policy.EgressDeny...), projectNetworkDenyHosts()...)
	return denyRulesExcludingHosts(deny, plan.allowedNetworkConflicts)
}

func denyRulesExcludingHosts(rules, hosts []string) []string {
	var kept []string
	for _, rule := range rules {
		if slices.ContainsFunc(hosts, func(host string) bool {
			return network.MatchesDestination(rule, host)
		}) {
			continue
		}
		kept = append(kept, rule)
	}
	return kept
}

// allowNetworkConflict removes the deny rules that match host from the launcher
// config and drops them from the in-memory deny source, allowing the host. It
// returns any removed rules that were broader than the host itself.
func allowNetworkConflict(plan *Plan, host string) ([]string, error) {
	removedBroad, err := removeLauncherNetworkDeny(plan, host)
	if err != nil {
		return nil, err
	}
	plan.allowedNetworkConflicts = appendUniqueHosts(plan.allowedNetworkConflicts, host)
	return removedBroad, nil
}

func removeLauncherNetworkDeny(plan *Plan, host string) ([]string, error) {
	path, data, err := planNetworkConfigSource(plan)
	if err != nil {
		return nil, err
	}
	config, err := configfile.Parse(path, data)
	if err != nil {
		return nil, err
	}
	var removedBroad []string
	networkValues, _ := config.Values()[launcherNetworkSection].(map[string]any)
	for _, rule := range stringList(networkValues[launcherEgressDenyKey]) {
		if rule != host && network.MatchesDestination(rule, host) {
			removedBroad = append(removedBroad, rule)
		}
	}
	config.FilterListItems([]string{launcherNetworkSection, launcherEgressDenyKey}, func(rule string) bool {
		return !network.MatchesDestination(rule, host)
	})
	updated, err := config.Marshal()
	if err != nil {
		return nil, err
	}
	plan.ConfigPath = path
	plan.ConfigData = updated
	if plan.configBaseData != nil {
		plan.configBaseData = updated
	}
	return appendUniqueHosts(nil, removedBroad...), nil
}

// choosePublicNetwork sets the launcher config to the public posture, which
// makes the conflicted hosts reachable without per-host egress rules. Deny is
// emitted before allow, so the deny rules that shadow the conflicted hosts are
// removed too; otherwise the emitted config would still block them while the
// plan reported the conflict resolved.
func choosePublicNetwork(plan *Plan) error {
	path, data, err := planNetworkConfigSource(plan)
	if err != nil {
		return err
	}
	config, err := configfile.Parse(path, data)
	if err != nil {
		return err
	}
	config.SetString([]string{launcherNetworkSection, "profile"}, network.ProfilePublic.String())
	removed := removeDenyRulesMatching(config, plan.NetworkConflicts)
	updated, err := config.Marshal()
	if err != nil {
		return err
	}
	plan.ConfigPath = path
	plan.ConfigData = updated
	if plan.configBaseData != nil {
		plan.configBaseData = updated
	}
	plan.policy.EgressDeny = hostsExcluding(plan.policy.EgressDeny, removed)
	plan.publicNetwork = true
	plan.NetworkConflicts = networkConflictsForHosts(plan.NetworkHosts, planNetworkDeny(plan))
	return nil
}

// removeDenyRulesMatching drops every egress-deny rule that shadows one of
// hosts from the launcher config and returns the removed rules.
func removeDenyRulesMatching(config *configfile.Config, hosts []string) []string {
	networkValues, _ := config.Values()[launcherNetworkSection].(map[string]any)
	var removed []string
	for _, rule := range stringList(networkValues[launcherEgressDenyKey]) {
		if conflictRuleMatchesHost(rule, hosts) {
			removed = append(removed, rule)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	config.FilterListItems([]string{launcherNetworkSection, launcherEgressDenyKey}, func(rule string) bool {
		return !conflictRuleMatchesHost(rule, hosts)
	})
	return appendUniqueHosts(nil, removed...)
}

func conflictRuleMatchesHost(rule string, hosts []string) bool {
	return slices.ContainsFunc(hosts, func(host string) bool {
		return network.MatchesDestination(rule, host)
	})
}

func networkConflictsForHosts(hosts, deny []string) []string {
	var conflicts []string
	for _, host := range hosts {
		if hostDeniedByAny(deny, host) {
			conflicts = append(conflicts, host)
		}
	}
	return conflicts
}

func planNetworkConfigSource(plan *Plan) (string, []byte, error) {
	if plan.configBaseData != nil {
		return plan.ConfigPath, plan.configBaseData, nil
	}
	if plan.ConfigPath != "" && plan.ConfigData != nil {
		return plan.ConfigPath, plan.ConfigData, nil
	}
	if plan.ConfigPath != "" {
		data, err := os.ReadFile(plan.ConfigPath)
		if err != nil {
			return "", nil, fmt.Errorf("read launcher config %s: %w", plan.ConfigPath, err)
		}
		return plan.ConfigPath, data, nil
	}
	return migrationLoadLauncherConfig()
}

// skipNetworkConflict drops host from the derived network hosts so it is not
// allowed, and remembers the choice for later re-derivation.
func skipNetworkConflict(plan *Plan, host string) {
	plan.RequiredNetworkHosts = hostsExcluding(plan.RequiredNetworkHosts, []string{host})
	plan.NetworkHosts = hostsExcluding(plan.NetworkHosts, []string{host})
	plan.NetworkConflicts = hostsExcluding(plan.NetworkConflicts, []string{host})
	plan.skippedNetworkHosts = appendUniqueHosts(plan.skippedNetworkHosts, host)
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
	path, data, err := configfile.LoadDir(cp.Get().ProjectConfigDir())
	if err != nil {
		return nil
	}
	var root map[string]any
	if configfile.IsJSON(path) {
		if json5.Unmarshal(data, &root) != nil {
			return nil
		}
	} else if yaml.Unmarshal(data, &root) != nil {
		return nil
	}
	networkConfig, ok := root[launcherNetworkSection].(map[string]any)
	if !ok {
		return nil
	}
	return stringList(networkConfig[launcherEgressDenyKey])
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

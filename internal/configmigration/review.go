package configmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// Review presents generated files and asks for confirmation of warnings and
// network policy changes before Apply writes anything.
func Review(plan *Plan, ui termio.UI) error {
	if plan == nil {
		return errors.New("nil migration plan")
	}
	if plan.AgentName == claudeCodeAgentName {
		if err := reviewClaudeAuthentication(plan, ui); err != nil {
			return err
		}
	}
	if err := prepareSecretReview(plan, ui); err != nil {
		return err
	}
	reportPlanWarnings(plan, ui)
	if err := confirmAuthFieldReview(plan, ui); err != nil {
		return err
	}
	if err := resolveNetworkConflicts(plan, ui); err != nil {
		return err
	}
	reportPlanNetwork(plan, ui)
	Report(plan, ui)
	return nil
}

const (
	networkConflictAllowChoice = "allow-anyway"
	networkConflictSkipChoice  = "skip"
	networkConflictAbortChoice = "abort"
)

// resolveNetworkConflicts asks the user how to handle derived egress hosts that
// are shadowed by egress-deny rules, then recomputes the launcher network.
func resolveNetworkConflicts(plan *Plan, ui termio.UI) error {
	if len(plan.NetworkConflicts) == 0 {
		return nil
	}
	if !ui.IsInteractive() {
		return fmt.Errorf(
			"network egress conflicts require interactive resolution: %s",
			strings.Join(plan.NetworkConflicts, ", "),
		)
	}
	conflicts := append([]string(nil), plan.NetworkConflicts...)
	for _, host := range conflicts {
		choice, err := ui.Select(
			fmt.Sprintf("Egress deny rules block required host %s; how should the migration proceed?", host),
			[]termio.Choice{
				{
					Key:         networkConflictAllowChoice,
					Label:       "Allow anyway",
					Description: "remove the matching egress-deny rule for " + host,
				},
				{
					Key:         networkConflictSkipChoice,
					Label:       "Skip",
					Description: "do not allow " + host,
				},
				{
					Key:         networkConflictAbortChoice,
					Label:       "Abort",
					Description: "leave the configuration unchanged",
				},
			},
			networkConflictSkipChoice,
		)
		if err != nil {
			return err
		}
		switch choice {
		case networkConflictAllowChoice:
			if err := allowNetworkConflict(plan, host); err != nil {
				return err
			}
		case networkConflictSkipChoice:
			skipNetworkConflict(plan, host)
		case networkConflictAbortChoice:
			return fmt.Errorf("migration aborted: egress deny rules block %s", host)
		default:
			return fmt.Errorf("unknown network conflict choice %q", choice)
		}
	}
	return refreshNetworkPlan(plan)
}

func prepareSecretReview(plan *Plan, ui termio.UI) error {
	secrets := cloneSecrets(plan.Secrets)
	if err := completeSecretHostsAtPath(secrets, plan.SecretPath, ui); err != nil {
		return err
	}
	plan.Secrets = secrets
	if err := refreshNetworkPlan(plan); err != nil {
		return err
	}
	if len(plan.Secrets) > 0 {
		ui.Outf("Secret allowlists written to %s:", plan.SecretPath)
		for _, name := range sortedSecretNames(plan.Secrets) {
			secret := plan.Secrets[name]
			hosts := append([]string{}, secret.Hosts...)
			if secret.Host != "" {
				hosts = append(hosts, secret.Host)
			}
			ui.Outf("  %s -> %s", name, strings.Join(hosts, ", "))
		}
	}
	return nil
}

func reportPlanWarnings(plan *Plan, ui termio.UI) {
	for _, warning := range plan.Warnings {
		ui.Warn(warning)
	}
	for _, warning := range plan.ReviewWarnings {
		ui.Warn(warning)
	}
}

func confirmAuthFieldReview(plan *Plan, ui termio.UI) error {
	if len(plan.ReviewWarnings) == 0 {
		return nil
	}
	if !ui.IsInteractive() {
		return errors.New("migration requires interactive review of unsupported auth fields")
	}
	choice, err := ui.Select(
		"Generated auth data contains fields that were not recognized as safe; review and continue?",
		[]termio.Choice{
			{Key: "y", Label: "Continue", Description: "apply after reviewing generated files"},
			{Key: "n", Label: "Abort", Description: "leave native configuration unchanged"},
		},
		"n",
	)
	if err != nil {
		return err
	}
	if choice != "y" {
		return errors.New("migration aborted after auth field review")
	}
	return nil
}

func reportPlanNetwork(plan *Plan, ui termio.UI) {
	if len(plan.ConfigData) > 0 {
		warnIfLauncherConfigLosesComments(plan.ConfigPath, ui)
	}
	if len(plan.NetworkAllowHosts) > 0 {
		ui.Outf(
			"VM egress will be allowed to: %s (derived from the confirmed secret destinations)",
			strings.Join(plan.NetworkAllowHosts, ", "),
		)
	}
}

// Report prints generated paths and non-secret generated file contents after a
// successful migration. Raw secret values are intentionally never printed.
func Report(plan *Plan, ui termio.UI) {
	reportMigrationFiles(plan, ui, "Generated migration files (not written yet):")
}

// warnIfLauncherConfigLosesComments warns that a JSON-family launcher config
// is rewritten as plain JSON; YAML configs keep their comments.
func warnIfLauncherConfigLosesComments(path string, ui termio.UI) {
	if migrationIsJSONConfig(path) {
		ui.Warnf("%s will be rewritten as plain JSON; its comments and formatting are not preserved", path)
	}
}

func reportMigrationFiles(plan *Plan, ui termio.UI, heading string) {
	ui.Out(heading)
	for _, file := range plan.Files {
		ui.Outf("%s:\n%s", file.Path, file.Data)
	}
	if len(plan.ConfigData) > 0 {
		ui.Outf("%s:\n%s", plan.ConfigPath, plan.ConfigData)
	}
	ui.Outf("%s (raw secret values are not printed)", plan.SecretPath)
}

func refreshNetworkPlan(plan *Plan) error {
	hosts := appendUniqueHosts(
		append([]string(nil), plan.RequiredNetworkHosts...),
		plannedNetworkHosts(plan.Secrets)...)
	plan.NetworkHosts = hostsExcluding(hosts, plan.skippedNetworkHosts)
	plan.NetworkAllowHosts = nil
	plan.NetworkConflicts = nil
	if len(plan.NetworkHosts) == 0 {
		return nil
	}
	path := plan.ConfigPath
	data := plan.ConfigData
	if plan.configBaseData != nil {
		data = plan.configBaseData
	}
	if path == "" {
		var err error
		path, data, err = migrationLoadLauncherConfig()
		if err != nil {
			return err
		}
	}
	updatedPath, updatedData, _, skipped, err := updateLauncherNetwork(
		path,
		data,
		plan.NetworkHosts,
		planNetworkDeny(plan),
	)
	if err != nil {
		return err
	}
	plan.ConfigPath = updatedPath
	plan.ConfigData = updatedData
	plan.NetworkAllowHosts = hostsExcluding(plan.NetworkHosts, skipped)
	plan.NetworkConflicts = skipped
	return nil
}

func reviewClaudeAuthentication(plan *Plan, ui termio.UI) error {
	if !ui.IsInteractive() {
		return nil
	}
	choice, err := ui.Select(
		"How should Claude Code authenticate inside the sandbox?",
		[]termio.Choice{
			{
				Key:         claudeAuthLoginChoice,
				Label:       "Normal Claude login",
				Description: "log in inside the sandbox after it starts",
			},
			{
				Key:         claudeAuthAPIKeyChoice,
				Label:       "Anthropic API key",
				Description: "store an API key in env.secret.yaml",
			},
		},
		claudeAuthLoginChoice,
	)
	if err != nil {
		return err
	}
	if choice == claudeAuthLoginChoice {
		ui.Info("Claude Code will use its normal login flow inside the sandbox.")
		return removeClaudeAuthentication(plan)
	}
	if choice != claudeAuthAPIKeyChoice {
		return fmt.Errorf("unknown Claude authentication choice %q", choice)
	}
	if removeErr := removeClaudeAuthentication(plan); removeErr != nil {
		return removeErr
	}
	endpoint, err := ui.Input("Claude API endpoint", "api.anthropic.com")
	if err != nil {
		return err
	}
	endpointHost, normalizedEndpoint, err := normalizeClaudeEndpoint(endpoint)
	if err != nil {
		return err
	}
	apiKey, err := ui.SecretInput("Anthropic API key")
	if err != nil {
		return err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("anthropic API key must not be empty")
	}
	ui.Info("Claude Code will use the API key through env.secret.yaml.")
	secretName := "CLAUDE_ENV_ANTHROPIC_" + "API_KEY"
	plan.Secrets[secretName] = Secret{ //nolint:exhaustruct // host policy is filled below
		Value: apiKey,
		Hosts: []string{endpointHost},
	}
	plan.RequiredNetworkHosts = appendUniqueHosts(plan.RequiredNetworkHosts, endpointHost)
	plan.RequiredNetworkHosts = appendUniqueHosts(plan.RequiredNetworkHosts, claudeAnthropicAPIHost)
	claude, ok := agent.Lookup(plan.AgentName)
	if !ok {
		return fmt.Errorf("unknown agent %q", plan.AgentName)
	}
	updatedFiles, err := appendClaudeSettingsEnv(plan.Files, claude, secretName, normalizedEndpoint)
	if err != nil {
		return err
	}
	plan.Files = updatedFiles
	return nil
}

func removeClaudeAuthentication(plan *Plan) error {
	for name := range plan.Secrets {
		if strings.HasPrefix(name, "CLAUDE_ENV_") {
			delete(plan.Secrets, name)
		}
	}
	for index := range plan.Files {
		if filepath.Base(plan.Files[index].Path) != "settings-migrated.json" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal(plan.Files[index].Data, &settings); err != nil {
			return fmt.Errorf("parse generated Claude settings: %w", err)
		}
		env, ok := settings["env"].(map[string]any)
		if !ok {
			continue
		}
		for name := range env {
			if claudeSecretVariable(name) {
				delete(env, name)
			}
		}
		data, err := marshalJSONIndentFn(settings, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal generated Claude settings: %w", err)
		}
		plan.Files[index].Data = append(append([]byte(nil), data...), '\n')
	}
	return nil
}

func appendClaudeSettingsEnv(files []File, claude agent.Agent, secretName, endpoint string) ([]File, error) {
	path := filepath.Join(cp.Get().UserAgentConfigDir(claude), "settings-migrated.json")
	settings := make(map[string]any)
	for _, file := range files {
		if file.Path != path {
			continue
		}
		if err := json.Unmarshal(file.Data, &settings); err != nil {
			return nil, fmt.Errorf("parse generated Claude settings: %w", err)
		}
		break
	}
	env, ok := settings["env"].(map[string]any)
	if !ok {
		env = make(map[string]any)
		settings["env"] = env
	}
	env["ANTHROPIC_API_KEY"] = "$MSB_" + secretName
	env["ANTHROPIC_BASE_URL"] = endpoint
	data, err := marshalJSONIndentFn(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal generated Claude settings: %w", err)
	}
	data = append(data, '\n')
	for index := range files {
		if files[index].Path == path {
			files[index].Data = data
			return files, nil
		}
	}
	return append(files, File{Path: path, Data: data, Mode: defaultConfigMode}), nil
}

func validatePlanOutputs(plan *Plan, secrets map[string]Secret) error {
	seen := make(map[string]struct{})
	paths := make([]string, 0, len(plan.Files)+2)
	for _, file := range plan.Files {
		paths = append(paths, file.Path)
	}
	if len(secrets) > 0 {
		paths = append(paths, plan.SecretPath)
	}
	if len(plan.ConfigData) > 0 {
		paths = append(paths, plan.ConfigPath)
	}
	for _, path := range paths {
		if path == "" {
			return errors.New("migration output path must not be empty")
		}
		if _, ok := seen[path]; ok {
			return fmt.Errorf("migration output path is duplicated: %s", path)
		}
		seen[path] = struct{}{}
		if info, err := os.Stat(filepath.Dir(path)); err == nil && !info.IsDir() {
			return fmt.Errorf("migration output parent is not a directory: %s", filepath.Dir(path))
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("inspect migration output parent %s: %w", filepath.Dir(path), err)
		}
	}
	return nil
}

func manifestOutputs(plan *Plan, secretData []byte) []migrationOutput {
	outputs := make([]migrationOutput, 0, len(plan.Files)+2)
	for _, file := range plan.Files {
		outputs = append(outputs, migrationOutput{Path: file.Path, SHA256: contentHash(file.Data)})
	}
	if len(secretData) > 0 {
		outputs = append(outputs, migrationOutput{Path: plan.SecretPath, SHA256: contentHash(secretData)})
	}
	if len(plan.ConfigData) > 0 {
		outputs = append(outputs, migrationOutput{Path: plan.ConfigPath, SHA256: contentHash(plan.ConfigData)})
	}
	return outputs
}

func contentHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func completeSecretHostsAtPath(secrets map[string]Secret, secretPath string, ui termio.UI) error {
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		secret := secrets[name]
		if len(secret.Hosts) > 0 || secret.AllowAnyHostDangerous {
			continue
		}
		host, err := ui.Input(
			fmt.Sprintf(
				"Allowed destination host for secret %s (written to %s; controls secret delivery)",
				name,
				secretPath,
			),
			"",
		)
		if err != nil {
			return fmt.Errorf("read allowed host for %s: %w", name, err)
		}
		host = strings.TrimSpace(host)
		if host == "" || strings.IndexFunc(host, unicode.IsSpace) >= 0 {
			return fmt.Errorf("secret %s needs a non-empty allowed host", name)
		}
		secret.Hosts = []string{host}
		secrets[name] = secret
	}
	return nil
}

func sortedSecretNames(secrets map[string]Secret) []string {
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Dismiss(plan *Plan) error {
	if plan == nil {
		return errors.New("nil migration plan")
	}
	return migrationWriteStatus(plan.AgentName, plan.SourceHash, true)
}

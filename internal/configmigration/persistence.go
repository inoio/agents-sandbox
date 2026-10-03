package configmigration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	defaultAuthMode         = 0o600
	defaultConfigMode       = 0o600
	defaultConfigFile       = "config.yaml"
	statusFileName          = "config-migration.yaml"
	extJSON                 = ".json"
	extJSONC                = ".jsonc"
	extJSON5                = ".json5"
	extYAML                 = ".yaml"
	extYML                  = ".yml"
	manifestVersion         = 1
	migrationStateApplying  = "applying"
	migrationStateCompleted = "completed"
	migrationStateDismissed = "dismissed"
)

//nolint:gochecknoglobals // package-local seams restored by tests
var (
	migrateNativeConfigFn             = migrateNativeConfig
	migrateNativeSupplementalConfigFn = migrateNativeSupplementalConfig
	sanitizeAuthFn                    = sanitizeAuth
	sanitizeAuthWithSpecFn            = sanitizeAuthWithSpec
	buildLauncherConfigFn             = buildLauncherConfig
	atomicWriteFn                     = migrationAtomicWrite
	enableNativeProvisioningFn        = EnableNativeProvisioning
	dismissMigrationFn                = Dismiss
	marshalJSONFn                     = json.Marshal
	marshalJSONIndentFn               = json.MarshalIndent
	marshalYAMLFn                     = yaml.Marshal
)

func migrationHomeMapping(root map[string]any, target string) (string, bool) {
	home, ok := root["home"].(map[string]any)
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

func migrationLoadLauncherConfig() (string, []byte, error) {
	dir := cp.Get().UserConfigDir()
	for _, ext := range []string{extYAML, extYML, extJSON, extJSONC, extJSON5} {
		path := filepath.Join(dir, "config"+ext)
		data, err := os.ReadFile(path)
		if err == nil {
			return path, data, nil
		}
		if !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("read launcher config %s: %w", path, err)
		}
	}
	return filepath.Join(dir, defaultConfigFile), []byte("{}\n"), nil
}

func migrationIsJSONConfig(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == extJSON || ext == extJSONC || ext == extJSON5
}

func migrationMergeSecretFile(path string, generated map[string]Secret) ([]byte, error) {
	secrets := make(map[string]Secret)
	if data, err := os.ReadFile(path); err == nil {
		if parseErr := yaml.Unmarshal(data, &secrets); parseErr != nil {
			return nil, fmt.Errorf("parse secret file %s: %w", path, parseErr)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read secret file %s: %w", path, err)
	}
	for name, generatedSecret := range generated {
		if existing, ok := secrets[name]; ok && !sameSecret(existing, generatedSecret) {
			return nil, fmt.Errorf("secret %s already exists with different contents", name)
		}
		secrets[name] = generatedSecret
	}
	data, err := marshalYAMLFn(secrets)
	if err != nil {
		return nil, fmt.Errorf("marshal secret file: %w", err)
	}
	return data, nil
}

func sameStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func sameSecret(a, b Secret) bool {
	return a.Value == b.Value &&
		a.Host == b.Host &&
		sameStrings(a.Hosts, b.Hosts) &&
		a.AllowAnyHostDangerous == b.AllowAnyHostDangerous
}

type migrationStatus struct {
	SourceHash string `yaml:"source_hash"`
	Handled    bool   `yaml:"handled"`
	Dismissed  bool   `yaml:"dismissed"`
}

type migrationManifest struct {
	Version    int               `yaml:"version"`
	State      string            `yaml:"state"`
	Agent      string            `yaml:"agent"`
	SourceHash string            `yaml:"source_hash"`
	Outputs    []migrationOutput `yaml:"outputs,omitempty"`
}

type migrationOutput struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

func migrationManifestPath(agentName string) string {
	return filepath.Join(cp.Get().UserStateDir(), agentName, statusFileName)
}

func migrationManifestFor(a interface{ Name() string }) migrationManifest {
	data, err := os.ReadFile(migrationManifestPath(a.Name()))
	if err != nil {
		return migrationManifest{}
	}
	var manifest migrationManifest
	if yaml.Unmarshal(data, &manifest) != nil {
		return migrationManifest{}
	}
	return manifest
}

func writeMigrationManifest(manifest migrationManifest) error {
	data, err := marshalYAMLFn(manifest)
	if err != nil {
		return err
	}
	return atomicWriteFn(migrationManifestPath(manifest.Agent), data, defaultAuthMode)
}

func migrationWriteStatus(agentName, sourceHash string, dismissed bool) error {
	state := migrationStateCompleted
	if dismissed {
		state = migrationStateDismissed
	}
	return writeMigrationManifest(migrationManifest{
		Version:    manifestVersion,
		State:      state,
		Agent:      agentName,
		SourceHash: sourceHash,
		Outputs:    nil,
	})
}

func migrationStatusPath(a interface{ Name() string }) string {
	return filepath.Join(cp.Get().UserStateDir(), a.Name(), statusFileName)
}

func migrationAtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agents-sandbox-migrate-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	return commitTempFile(tmp, path, data, mode)
}

// commitTempFile writes data to the temporary file, syncs and closes it, and
// moves it to path. The file is always closed, also when an earlier step fails.
func commitTempFile(tmp *os.File, path string, data []byte, mode os.FileMode) error {
	chmodErr := tmp.Chmod(mode)
	_, writeErr := tmp.Write(data)
	if err := errors.Join(chmodErr, writeErr, tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func migrationStatusFor(a interface{ Name() string }, sourceHash string) (bool, bool) {
	manifest := migrationManifestFor(a)
	if manifest.Version == 0 {
		data, err := os.ReadFile(migrationStatusPath(a))
		if err != nil {
			return false, false
		}
		var legacy migrationStatus
		if yaml.Unmarshal(data, &legacy) == nil && legacy.SourceHash == sourceHash {
			return legacy.Handled, legacy.Dismissed
		}
	}
	if manifest.SourceHash != sourceHash {
		return false, false
	}
	return manifest.State == migrationStateCompleted || manifest.State == migrationStateDismissed,
		manifest.State == migrationStateDismissed
}

func migrationAlreadyConfigured( //nolint:gocognit // validates independent output and mapping states
	a agent.Agent,
	spec agent.ConfigMigrationSpec,
	managedAuthPath, managedConfigPath, configPath string,
	authRequired, configRequired bool,
	supplementalNames []string,
) bool {
	if authRequired {
		if _, err := os.Stat(managedAuthPath); err != nil {
			return false
		}
	}
	if configRequired && spec.ManagedSnippet != "" {
		if _, err := os.Stat(managedConfigPath); err != nil {
			return false
		}
	}
	for _, name := range supplementalNames {
		if _, err := os.Stat(filepath.Join(filepath.Dir(managedConfigPath), name)); err != nil {
			return false
		}
	}
	_, data, err := migrationLoadLauncherConfig()
	if err != nil {
		return false
	}
	var root map[string]any
	if migrationIsJSONConfig(configPath) {
		if unmarshalErr := json5.Unmarshal(data, &root); unmarshalErr != nil {
			return false
		}
	} else if unmarshalErr := yaml.Unmarshal(data, &root); unmarshalErr != nil {
		return false
	}
	mappings := map[string]string{}
	if authRequired {
		mappings[spec.CredentialTarget] = filepath.Join(a.ConfigDirName(), spec.ManagedCredential)
	}
	for _, name := range supplementalNames {
		mappings[filepath.Join(filepath.Dir(spec.CredentialTarget), name)] = filepath.Join(a.ConfigDirName(), name)
	}
	for target, source := range mappings {
		mapping, ok := migrationHomeMapping(root, target)
		if !ok || mapping != source {
			return false
		}
	}
	return true
}

//nolint:funlen,gocognit // migration planning keeps all output decisions together
func Build(
	a agent.Agent,
	hostHome string,
) (*Plan, error) {
	provider, ok := agent.AsMigrationSpecProvider(a)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, a.Name())
	}
	spec := provider.MigrationSpec()
	nativeFiles, err := collectNativeFiles(hostHome, spec)
	if err != nil {
		return nil, err
	}
	retrying := migrationManifestFor(a).State == migrationStateApplying &&
		migrationManifestFor(a).SourceHash == hashNativeFiles(nativeFiles)
	result := &Plan{ //nolint:exhaustruct // remaining fields are populated during planning
		AgentName:        a.Name(),
		Secrets:          make(map[string]Secret),
		SourceHash:       hashNativeFiles(nativeFiles),
		HasNativeConfig:  len(nativeFiles) > 0,
		HasManagedConfig: hasManagedConfig(a, spec) && !retrying,
		SecretPath:       cp.Get().UserEnvSecretYAMLFile(),
		Retrying:         retrying,
	}
	if !result.HasNativeConfig {
		return result, nil
	}
	configPath := filepath.Join(cp.Get().UserConfigDir(), defaultConfigFile)
	if launcherPath, _, loadErr := migrationLoadLauncherConfig(); loadErr == nil {
		configPath = launcherPath
	}
	_, nativeAuthPath := nativeAgentPaths(hostHome, spec)
	managedAuthPath := filepath.Join(cp.Get().UserAgentConfigDir(a), spec.ManagedCredential)
	managedConfigPath := filepath.Join(cp.Get().UserAgentConfigDir(a), spec.ManagedSnippet)
	configData, configSecrets, configWarnings, providerHosts, configErr := migrateNativeConfigFn(hostHome, spec)
	if configErr != nil {
		return nil, configErr
	}
	if len(configData) > 0 {
		result.Files = append(result.Files, File{Path: managedConfigPath, Data: configData, Mode: defaultConfigMode})
		maps.Copy(result.Secrets, configSecrets)
		result.Warnings = append(result.Warnings, configWarnings...)
	}
	supplementalFiles, supplementalSecrets, supplementalWarnings, supplementalHosts, supplementalErr :=
		migrateNativeSupplementalConfigFn(hostHome, spec)
	if supplementalErr != nil {
		return nil, supplementalErr
	}
	supplementalNames := make([]string, 0, len(supplementalFiles))
	for name := range supplementalFiles {
		supplementalNames = append(supplementalNames, name)
	}
	sort.Strings(supplementalNames)
	for _, name := range supplementalNames {
		result.Files = append(result.Files, File{
			Path: filepath.Join(cp.Get().UserAgentConfigDir(a), name),
			Data: supplementalFiles[name],
			Mode: defaultConfigMode,
		})
	}
	if err := mergePlannedSecrets(result.Secrets, supplementalSecrets); err != nil {
		return nil, err
	}
	result.Warnings = append(result.Warnings, supplementalWarnings...)
	if providerHosts == nil {
		providerHosts = make(map[string]string)
	}
	maps.Copy(providerHosts, supplementalHosts)
	if err := applyAuthMigration(
		result,
		spec,
		nativeFiles,
		nativeAuthPath,
		managedAuthPath,
		providerHosts,
	); err != nil {
		return nil, err
	}
	result.NetworkHosts = plannedNetworkHosts(result.Secrets)
	authGenerated := hasGeneratedCredential(result.Files, spec.ManagedCredential)
	supplementalNames = generatedSupplementalNames(result.Files, spec.ManagedSupplementalFiles)
	launcherSpec := spec
	launcherSpec.ManagedSupplementalFiles = supplementalNames
	if !authGenerated {
		launcherSpec.ManagedCredential = ""
	}
	needsCredentialMapping := authGenerated || len(supplementalNames) > 0
	if needsCredentialMapping || len(result.NetworkHosts) > 0 {
		launcherPath, launcherData, launcherErr := buildLauncherConfigForPlan(a, launcherSpec, needsCredentialMapping)
		if launcherErr != nil {
			return nil, launcherErr
		}
		launcherPath, launcherData, networkAllowHosts, launcherErr := updateLauncherNetwork(
			launcherPath,
			launcherData,
			result.NetworkHosts,
		)
		if launcherErr != nil {
			return nil, launcherErr
		}
		result.ConfigPath = launcherPath
		result.ConfigData = launcherData
		_ = networkAllowHosts
		result.NetworkAllowHosts = result.NetworkHosts
	}
	result.Handled, result.Dismissed = migrationStatusFor(a, result.SourceHash)
	result.AlreadyConfigured = result.Handled && migrationAlreadyConfigured(
		a, spec, managedAuthPath, managedConfigPath, configPath, authGenerated, len(configData) > 0, supplementalNames,
	)
	if result.AlreadyConfigured {
		result.Warnings = append(result.Warnings, "managed configuration already contains a safe migration")
	}
	if len(nativeFiles) > 0 && len(result.Files) == 0 {
		result.Warnings = append(
			result.Warnings,
			"native agent settings were found but are not copied automatically; review them and add managed snippets manually",
		)
	}
	return result, nil
}

func applyAuthMigration(
	result *Plan,
	spec agent.ConfigMigrationSpec,
	nativeFiles []nativeFile,
	nativeAuthPath, managedAuthPath string,
	providerHosts map[string]string,
) error {
	authData, ok := nativeFileData(nativeFiles, nativeAuthPath)
	if !ok {
		return nil
	}
	generated, secrets, warnings, transformErr := sanitizeAuthWithSpecFn(
		authData,
		spec,
		spec.KnownProviderHosts,
		providerHosts,
	)
	if transformErr != nil {
		return fmt.Errorf("migrate %s: %w", nativeAuthPath, transformErr)
	}
	if len(generated) == 0 {
		return nil
	}
	result.Files = append(result.Files, File{Path: managedAuthPath, Data: generated, Mode: defaultAuthMode})
	if mergeErr := mergePlannedSecrets(result.Secrets, secrets); mergeErr != nil {
		return mergeErr
	}
	result.Warnings = append(result.Warnings, warnings...)
	for _, warning := range warnings {
		if strings.HasPrefix(warning, "review required:") {
			result.ReviewWarnings = append(result.ReviewWarnings, warning)
		}
	}
	return nil
}

func hasGeneratedCredential(files []File, credentialName string) bool {
	for _, file := range files {
		if filepath.Base(file.Path) == credentialName {
			return true
		}
	}
	return false
}

func generatedSupplementalNames(files []File, names []string) []string {
	var generated []string
	for _, name := range names {
		if hasGeneratedCredential(files, name) {
			generated = append(generated, name)
		}
	}
	return generated
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

func updateLauncherNetwork(path string, data []byte, hosts []string) (string, []byte, []string, error) {
	if len(hosts) == 0 {
		return path, data, nil, nil
	}
	var root map[string]any
	if migrationIsJSONConfig(path) {
		if err := json5.Unmarshal(data, &root); err != nil {
			return "", nil, nil, fmt.Errorf("parse launcher config %s: %w", path, err)
		}
	} else if err := yaml.Unmarshal(data, &root); err != nil {
		return "", nil, nil, fmt.Errorf("parse launcher config %s: %w", path, err)
	}
	if root == nil {
		root = make(map[string]any)
	}
	networkConfig, ok := root["network"].(map[string]any)
	if !ok {
		networkConfig = make(map[string]any)
		root["network"] = networkConfig
	}
	allow := stringList(networkConfig["egress-allow"])
	deny := stringList(networkConfig["egress-deny"])
	deny = append(deny, projectNetworkDenyHosts()...)
	var added []string
	for _, host := range hosts {
		for _, blocked := range deny {
			if blocked == host || blocked == "*" {
				return "", nil, nil, fmt.Errorf("network host %q is denied by network.egress-deny", host)
			}
		}
		if !slicesContains(allow, host) {
			allow = append(allow, host)
			added = append(added, host)
		}
	}
	sort.Strings(allow)
	networkConfig["egress-allow"] = allow
	if migrationIsJSONConfig(path) {
		updated, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return "", nil, nil, fmt.Errorf("marshal launcher config: %w", err)
		}
		return path, append(updated, '\n'), added, nil
	}
	updated, err := yaml.Marshal(root)
	if err != nil {
		return "", nil, nil, fmt.Errorf("marshal launcher config: %w", err)
	}
	return path, updated, added, nil
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

func slicesContains(values []string, wanted string) bool {
	return slices.Contains(values, wanted)
}

func nativeFileData(files []nativeFile, path string) ([]byte, bool) {
	for _, file := range files {
		if file.Path == path {
			return file.Data, true
		}
	}
	return nil, false
}

func Apply(plan *Plan, ui termio.UI) error {
	if plan == nil {
		return errors.New("nil migration plan")
	}
	secrets := cloneSecrets(plan.Secrets)
	if err := completeSecretHostsAtPath(secrets, plan.SecretPath, ui); err != nil {
		return err
	}
	secretData, err := migrationMergeSecretFile(plan.SecretPath, secrets)
	if err != nil {
		return err
	}
	if err := validatePlanOutputs(plan, secrets); err != nil {
		return err
	}
	outputs := manifestOutputs(plan, secretData)
	if err := writeMigrationManifest(migrationManifest{
		Version:    manifestVersion,
		State:      migrationStateApplying,
		Agent:      plan.AgentName,
		SourceHash: plan.SourceHash,
		Outputs:    outputs,
	}); err != nil {
		return fmt.Errorf("record migration state: %w", err)
	}
	for _, file := range plan.Files {
		if err := writeIfChanged(file.Path, file.Data, file.Mode); err != nil {
			return fmt.Errorf("write migrated file %s: %w", file.Path, err)
		}
	}
	if len(secrets) > 0 {
		if err := writeIfChanged(plan.SecretPath, secretData, defaultAuthMode); err != nil {
			return fmt.Errorf("write migrated secrets: %w", err)
		}
	}
	if len(plan.ConfigData) > 0 {
		if err := writeIfChanged(plan.ConfigPath, plan.ConfigData, defaultConfigMode); err != nil {
			return fmt.Errorf("write launcher config: %w", err)
		}
	}
	if err := writeMigrationManifest(migrationManifest{
		Version:    manifestVersion,
		State:      migrationStateCompleted,
		Agent:      plan.AgentName,
		SourceHash: plan.SourceHash,
		Outputs:    outputs,
	}); err != nil {
		return fmt.Errorf("complete migration state: %w", err)
	}
	return nil
}

func writeIfChanged(path string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return atomicWriteFn(path, data, mode)
}

// Review presents generated files and asks for confirmation of warnings and
// network policy changes before Apply writes anything.
func Review(plan *Plan, ui termio.UI) error {
	if plan == nil {
		return errors.New("nil migration plan")
	}
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
	for _, warning := range plan.Warnings {
		ui.Warn(warning)
	}
	for _, warning := range plan.ReviewWarnings {
		ui.Warn(warning)
	}
	if len(plan.ReviewWarnings) > 0 {
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
	}
	if len(plan.NetworkAllowHosts) > 0 {
		ui.Outf(
			"VM egress will be allowed to: %s (derived from the confirmed secret destinations)",
			strings.Join(plan.NetworkAllowHosts, ", "),
		)
	}
	Report(plan, ui)
	return nil
}

// Report prints generated paths and non-secret generated file contents after a
// successful migration. Raw secret values are intentionally never printed.
func Report(plan *Plan, ui termio.UI) {
	reportMigrationFiles(plan, ui, "Generated migration files (not written yet):")
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
	plan.NetworkHosts = plannedNetworkHosts(plan.Secrets)
	if len(plan.NetworkHosts) == 0 {
		return nil
	}
	path := plan.ConfigPath
	data := plan.ConfigData
	if path == "" {
		var err error
		path, data, err = migrationLoadLauncherConfig()
		if err != nil {
			return err
		}
	}
	updatedPath, updatedData, added, err := updateLauncherNetwork(path, data, plan.NetworkHosts)
	if err != nil {
		return err
	}
	plan.ConfigPath = updatedPath
	plan.ConfigData = updatedData
	_ = added
	plan.NetworkAllowHosts = plan.NetworkHosts
	return nil
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

//nolint:gocognit // user-facing migration choices intentionally remain visible
func Guide(
	a agent.Agent,
	hostHome string,
	provisionHostConfig bool,
	ui termio.UI,
) error {
	if provisionHostConfig {
		return nil
	}
	if !ui.IsInteractive() {
		provider, supported := agent.AsMigrationSpecProvider(a)
		managed := false
		if supported {
			managed = hasManagedConfig(a, provider.MigrationSpec())
		}
		if nativeConfigExists(a, hostHome) && !managed {
			ui.Warnf(
				"native %s configuration was found but was not migrated; run 'agents-sandbox config migrate' interactively",
				a.Name(),
			)
		}
		return nil
	}
	plan, err := Build(a, hostHome)
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return nil
		}
		return err
	}
	if !plan.HasChanges() {
		return nil
	}
	ui.Warnf("native %s configuration was found, but host-config provisioning is disabled", a.Name())
	key, err := ui.Select("Choose how to configure the sandbox", []termio.Choice{
		{
			Key:         "m",
			Label:       "Migrate safely",
			Description: "replace supported credentials with microsandbox placeholders",
		},
		{
			Key:         "p",
			Label:       "Use native host config",
			Description: "enable copying raw host config and credentials into the VM",
		},
		{Key: "d", Label: "Continue without migration", Description: "configure the agent manually later"},
	}, "m")
	if err != nil {
		return err
	}
	switch key {
	case "m":
		if err := Review(plan, ui); err != nil {
			return err
		}
		if err := Apply(plan, ui); err != nil {
			return err
		}
		ui.Infof("safe %s configuration migration completed", a.Name())
		ui.Info("")
		choice, err := ui.Select(
			"Start agents-sandbox now? The files are already written and can be reviewed or edited either way.",
			[]termio.Choice{
				{Key: "y", Label: "Start now", Description: "start the sandbox with the generated configuration"},
				{
					Key:         "n",
					Label:       "Quit",
					Description: "exit (and keep generated files)",
				},
			},
			"y",
		)
		if err != nil {
			return err
		}
		if choice == "n" {
			return ErrStartDeferred
		}
		if choice != "y" {
			return fmt.Errorf("unknown start choice %q", choice)
		}
		return nil
	case "p":
		if err := enableNativeProvisioningFn(); err != nil {
			return err
		}
		ui.Warn("native host-config provisioning is now enabled; rerun agents-sandbox to apply it")
		return ErrRerunRequired
	case "d":
		if err := dismissMigrationFn(plan); err != nil {
			return err
		}
		ui.Info("continuing without migrated agent configuration")
		return nil
	default:
		return fmt.Errorf("unknown migration choice %q", key)
	}
}

func nativeConfigExists(a agent.Agent, hostHome string) bool {
	provider, ok := agent.AsMigrationSpecProvider(a)
	if !ok {
		return false
	}
	spec := provider.MigrationSpec()
	configDir, authPath := nativeAgentPaths(hostHome, spec)
	pathsToCheck := append([]string{}, spec.NativeConfigFiles...)
	pathsToCheck = append(pathsToCheck, spec.NativeSupplementalFiles...)
	for _, name := range pathsToCheck {
		if _, err := os.Stat(filepath.Join(configDir, name)); err == nil {
			return true
		}
	}
	_, err := os.Stat(authPath)
	return err == nil
}

func EnableNativeProvisioning() error {
	path, data, err := migrationLoadLauncherConfig()
	if err != nil {
		return err
	}
	var root map[string]any
	if migrationIsJSONConfig(path) {
		if unmarshalErr := json5.Unmarshal(data, &root); unmarshalErr != nil {
			return fmt.Errorf("parse launcher config: %w", unmarshalErr)
		}
	} else if unmarshalErr := yaml.Unmarshal(data, &root); unmarshalErr != nil {
		return fmt.Errorf("parse launcher config: %w", unmarshalErr)
	}
	if root == nil {
		root = make(map[string]any)
	}
	root["provision-host-config"] = true
	var updated []byte
	if migrationIsJSONConfig(path) {
		updated, err = json.MarshalIndent(root, "", "  ")
		updated = append(updated, '\n')
	} else {
		updated, err = yaml.Marshal(root)
	}
	if err != nil {
		return fmt.Errorf("marshal launcher config: %w", err)
	}
	return atomicWriteFn(path, updated, defaultConfigMode)
}

type nativeFile struct {
	Path string
	Data []byte
}

func collectNativeFiles(hostHome string, spec agent.ConfigMigrationSpec) ([]nativeFile, error) {
	var files []nativeFile
	configDir, authPath := nativeAgentPaths(hostHome, spec)
	paths := make([]string, 0, len(spec.NativeConfigFiles)+len(spec.NativeSupplementalFiles)+1)
	for _, name := range append(spec.NativeConfigFiles, spec.NativeSupplementalFiles...) {
		paths = append(paths, filepath.Join(configDir, name))
	}
	paths = append(paths, authPath)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect native config %s: %w", path, err)
		}
		files = append(files, nativeFile{Path: path, Data: data})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func nativeAgentPaths(hostHome string, spec agent.ConfigMigrationSpec) (string, string) {
	configDir := filepath.Join(hostHome, spec.NativeConfigDir)
	if spec.NativeConfigEnv != "" {
		if value := os.Getenv(spec.NativeConfigEnv); value != "" {
			configDir = migrationEnvPath(hostHome, value, spec.NativeConfigEnvIsPath, spec.NativeConfigSubdir)
		} else if !spec.NativeConfigEnvIsPath {
			configDir = filepath.Join(hostHome, filepath.Dir(spec.NativeConfigDir), spec.NativeConfigSubdir)
		}
	}

	credentialDir := filepath.Join(hostHome, filepath.Dir(spec.NativeCredential))
	if spec.NativeDataEnv != "" {
		if value := os.Getenv(spec.NativeDataEnv); value != "" {
			credentialDir = migrationEnvPath(hostHome, value, spec.NativeDataEnvIsPath, spec.NativeDataSubdir)
		} else if !spec.NativeDataEnvIsPath {
			credentialDir = filepath.Join(
				hostHome,
				filepath.Dir(filepath.Dir(spec.NativeCredential)),
				spec.NativeDataSubdir,
			)
		}
	}
	return configDir, filepath.Join(credentialDir, filepath.Base(spec.NativeCredential))
}

func migrationEnvPath(hostHome, value string, isPath bool, subdir string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "~/")
	if !filepath.IsAbs(value) {
		value = filepath.Join(hostHome, value)
	}
	if isPath {
		return value
	}
	return filepath.Join(value, subdir)
}

func hasManagedConfig(a agent.Agent, spec agent.ConfigMigrationSpec) bool {
	_, ok := agent.AsConfigMerger(a)
	if !ok {
		return false
	}
	for _, dir := range []string{cp.Get().UserAgentConfigDir(a), cp.Get().ProjectAgentConfigDir(a)} {
		for _, name := range append(
			[]string{spec.ManagedCredential, spec.ManagedSnippet},
			spec.ManagedSupplementalFiles...,
		) {
			if name == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
	}
	return false
}

func hashNativeFiles(files []nativeFile) string {
	h := sha256.New()
	for _, file := range files {
		_, _ = h.Write([]byte(file.Path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(file.Data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

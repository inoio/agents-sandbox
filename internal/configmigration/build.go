package configmigration

import (
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

func Build(
	a agent.Agent,
	hostHome string,
	policy network.Policy,
) (*Plan, error) {
	spec, err := migrationProviderSpec(a)
	if err != nil {
		return nil, err
	}
	nativeFiles, err := collectNativeFiles(hostHome, spec)
	if err != nil {
		return nil, err
	}
	result := newMigrationPlan(a, spec, nativeFiles, policy)
	if !result.HasNativeConfig {
		if networkErr := applyRequiredNetworkPlan(result); networkErr != nil {
			return nil, networkErr
		}
		result.Handled, result.Dismissed = migrationStatusFor(a, result.SourceHash)
		return result, nil
	}
	supplementalNames, configRequired, err := applyNativeConfigMigration(a, result, spec, nativeFiles, hostHome)
	if err != nil {
		return nil, err
	}
	if launcherErr := applyLauncherPlan(a, result, spec); launcherErr != nil {
		return nil, launcherErr
	}
	finalizeMigrationPlan(a, result, spec, migrationLauncherConfigPath(), configRequired, supplementalNames)
	return result, nil
}

func migrationProviderSpec(a agent.Agent) (agent.ConfigMigrationSpec, error) {
	provider, ok := agent.AsMigrationSpecProvider(a)
	if !ok {
		return agent.ConfigMigrationSpec{}, fmt.Errorf("%w: %s", ErrUnsupported, a.Name())
	}
	return provider.MigrationSpec(), nil
}

func newMigrationPlan(
	a agent.Agent,
	spec agent.ConfigMigrationSpec,
	nativeFiles []nativeFile,
	policy network.Policy,
) *Plan {
	manifest := migrationManifestFor(a)
	sourceHash := hashNativeFiles(nativeFiles)
	retrying := manifest.State == migrationStateApplying && manifest.SourceHash == sourceHash
	return &Plan{ //nolint:exhaustruct_v5 // remaining fields are populated during planning
		AgentName:            a.Name(),
		Secrets:              make(map[string]Secret),
		SourceHash:           sourceHash,
		HasNativeConfig:      len(nativeFiles) > 0,
		HasManagedConfig:     hasManagedConfig(a, spec) && !retrying,
		SecretPath:           cp.Get().UserEnvSecretYAMLFile(),
		Retrying:             retrying,
		RequiredNetworkHosts: append([]string(nil), spec.RequiredNetworkHosts...),
		SetupOnly:            a.Name() == claudeCodeAgentName && len(nativeFiles) == 0,
		policy:               policy,
	}
}

func applyNativeConfigMigration(
	a agent.Agent,
	result *Plan,
	spec agent.ConfigMigrationSpec,
	nativeFiles []nativeFile,
	hostHome string,
) ([]string, bool, error) {
	_, nativeAuthPath := nativeAgentPaths(hostHome, spec)
	managedAuthPath := filepath.Join(cp.Get().UserAgentConfigDir(a), spec.ManagedCredential)
	managedConfigPath := filepath.Join(cp.Get().UserAgentConfigDir(a), spec.ManagedSnippet)
	configData, configSecrets, configWarnings, providerHosts, configErr := migrateNativeConfigFn(hostHome, spec)
	if configErr != nil {
		return nil, false, configErr
	}
	if len(configData) > 0 {
		result.Files = append(result.Files, File{Path: managedConfigPath, Data: configData, Mode: defaultConfigMode})
		maps.Copy(result.Secrets, configSecrets)
	}
	result.Warnings = append(result.Warnings, configWarnings...)
	supplementalFiles, supplementalSecrets, supplementalWarnings, supplementalHosts, supplementalErr :=
		migrateNativeSupplementalConfigFn(hostHome, spec)
	if supplementalErr != nil {
		return nil, false, supplementalErr
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
		return nil, false, err
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
		return nil, false, err
	}
	result.NetworkHosts = appendUniqueHosts(
		result.RequiredNetworkHosts,
		append(plannedNetworkHosts(result.Secrets), result.LoginHosts...)...,
	)
	return supplementalNames, len(configData) > 0, nil
}

func applyLauncherPlan(a agent.Agent, result *Plan, spec agent.ConfigMigrationSpec) error {
	authGenerated := hasGeneratedCredential(result.Files, spec.ManagedCredential)
	supplementalNames := generatedSupplementalNames(result.Files, spec.ManagedSupplementalFiles)
	launcherSpec := spec
	launcherSpec.ManagedSupplementalFiles = supplementalNames
	if !authGenerated {
		launcherSpec.ManagedCredential = ""
	}
	needsCredentialMapping := authGenerated || len(supplementalNames) > 0
	if !needsCredentialMapping && len(result.NetworkHosts) == 0 {
		return nil
	}
	launcherPath, launcherData, launcherErr := buildLauncherConfigForPlan(a, launcherSpec, needsCredentialMapping)
	if launcherErr != nil {
		return launcherErr
	}
	result.configBaseData = launcherData
	launcherPath, launcherData, _, skipped, launcherErr := updateLauncherNetwork(
		launcherPath,
		launcherData,
		result.NetworkHosts,
		planNetworkDeny(result),
	)
	if launcherErr != nil {
		return launcherErr
	}
	result.ConfigPath = launcherPath
	result.ConfigData = launcherData
	result.NetworkAllowHosts = hostsExcluding(result.NetworkHosts, skipped)
	result.NetworkConflicts = skipped
	return nil
}

func finalizeMigrationPlan(
	a agent.Agent,
	result *Plan,
	spec agent.ConfigMigrationSpec,
	configPath string,
	configRequired bool,
	supplementalNames []string,
) {
	managedAuthPath := filepath.Join(cp.Get().UserAgentConfigDir(a), spec.ManagedCredential)
	managedConfigPath := filepath.Join(cp.Get().UserAgentConfigDir(a), spec.ManagedSnippet)
	result.Handled, result.Dismissed = migrationStatusFor(a, result.SourceHash)
	result.AlreadyConfigured = result.Handled && migrationAlreadyConfigured(
		a, spec, managedAuthPath, managedConfigPath, configPath,
		hasGeneratedCredential(result.Files, spec.ManagedCredential), configRequired, supplementalNames,
	)
	if result.AlreadyConfigured {
		result.Warnings = append(result.Warnings, "managed configuration already contains a safe migration")
	}
	if len(result.Files) == 0 {
		result.Warnings = append(
			result.Warnings,
			"native agent settings were found but are not copied automatically; review them and add managed snippets manually",
		)
	}
}

func applyRequiredNetworkPlan(plan *Plan) error {
	plan.NetworkHosts = appendUniqueHosts(append([]string(nil), plan.RequiredNetworkHosts...))
	if len(plan.NetworkHosts) == 0 {
		return nil
	}
	path, data, err := migrationLoadLauncherConfig()
	if err != nil {
		return err
	}
	plan.configBaseData = data
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
	generated, secrets, warnings, login, transformErr := sanitizeAuthWithSpecFn(
		authData,
		spec,
		spec.KnownProviderHosts,
		providerHosts,
	)
	if transformErr != nil {
		return fmt.Errorf("migrate %s: %w", nativeAuthPath, transformErr)
	}
	result.Warnings = append(result.Warnings, warnings...)
	for _, warning := range warnings {
		if strings.HasPrefix(warning, "review required:") {
			result.ReviewWarnings = append(result.ReviewWarnings, warning)
		}
	}
	applyLoginProviders(result, login)
	if len(generated) == 0 {
		return nil
	}
	result.Files = append(result.Files, File{Path: managedAuthPath, Data: generated, Mode: defaultAuthMode})
	if mergeErr := mergePlannedSecrets(result.Secrets, secrets); mergeErr != nil {
		return mergeErr
	}
	return nil
}

func applyLoginProviders(result *Plan, login []LoginProvider) {
	if len(login) == 0 {
		return
	}
	seenProviders := make(map[string]struct{}, len(login))
	for _, provider := range login {
		if _, ok := seenProviders[provider.Name]; !ok {
			seenProviders[provider.Name] = struct{}{}
			result.LoginProviders = append(result.LoginProviders, provider.Name)
		}
		result.LoginHosts = appendUniqueHosts(result.LoginHosts, provider.Hosts...)
	}
	sort.Strings(result.LoginProviders)
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

func nativeFileData(files []nativeFile, path string) ([]byte, bool) {
	for _, file := range files {
		if file.Path == path {
			return file.Data, true
		}
	}
	return nil, false
}

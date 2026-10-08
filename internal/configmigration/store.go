package configmigration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
)

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
	Pending    []pendingLogin    `yaml:"pending,omitempty"`
}

type pendingLogin struct {
	Provider string   `yaml:"provider"`
	Hosts    []string `yaml:"hosts,omitempty"`
}

type migrationOutput struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

func migrationManifestPath(agentName string) string {
	return filepath.Join(cp.Get().UserStateDir(), agentName, statusFileName)
}

func migrationManifestFor(a agent.Agent) migrationManifest {
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

func migrationWriteDecision(agentName, sourceHash, state string, pending []pendingLogin) error {
	return writeMigrationManifest(migrationManifest{
		Version:    manifestVersion,
		State:      state,
		Agent:      agentName,
		SourceHash: sourceHash,
		Outputs:    nil,
		Pending:    pending,
	})
}

func migrationWriteStatus(agentName, sourceHash string, dismissed bool) error {
	state := migrationStateCompleted
	if dismissed {
		state = migrationStateDismissed
	}
	return migrationWriteDecision(agentName, sourceHash, state, nil)
}

// RecordLoginPending persists the login-pending decision together with the
// providers whose in-sandbox login must be verified on a later run.
func RecordLoginPending(plan *Plan) error {
	return migrationWriteDecision(
		plan.AgentName,
		plan.SourceHash,
		migrationStateLoginPending,
		pendingLoginsFromPlan(plan),
	)
}

func pendingLoginsFromPlan(plan *Plan) []pendingLogin {
	logins := make([]pendingLogin, 0, len(plan.PendingLogins))
	for _, provider := range plan.PendingLogins {
		logins = append(logins, pendingLogin{
			Provider: provider.Name,
			Hosts:    provider.Hosts,
		})
	}
	sort.Slice(logins, func(i, j int) bool { return logins[i].Provider < logins[j].Provider })
	return logins
}

func pendingLoginsFromManifest(a agent.Agent) []LoginProvider {
	manifest := migrationManifestFor(a)
	logins := make([]LoginProvider, 0, len(manifest.Pending))
	for _, pending := range manifest.Pending {
		logins = append(logins, LoginProvider{Name: pending.Provider, Hosts: pending.Hosts})
	}
	return logins
}

// Complete persists the completed onboarding decision once the user confirms
// that the in-sandbox login succeeded.
func Complete(plan *Plan) error {
	return migrationWriteDecision(plan.AgentName, plan.SourceHash, migrationStateCompleted, nil)
}

// RecordManual persists the manual onboarding decision so later runs stay
// quiet until the native configuration changes.
func RecordManual(plan *Plan) error {
	return migrationWriteDecision(plan.AgentName, plan.SourceHash, migrationStateManual, nil)
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

func migrationStateFor(a agent.Agent, sourceHash string) string {
	manifest := migrationManifestFor(a)
	if manifest.Version == 0 || manifest.SourceHash != sourceHash {
		return ""
	}
	return manifest.State
}

func migrationStatusFor(a agent.Agent, sourceHash string) (bool, bool) {
	manifest := migrationManifestFor(a)
	if manifest.Version == 0 {
		data, err := os.ReadFile(migrationManifestPath(a.Name()))
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

func migrationAlreadyConfigured(
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
	for target, source := range migrationHomeMappings(a, spec, authRequired, supplementalNames) {
		mapping, ok := migrationHomeMapping(root, target)
		if !ok || mapping != source {
			return false
		}
	}
	return true
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

func migrationLauncherConfigPath() string {
	if path, _, err := migrationLoadLauncherConfig(); err == nil {
		return path
	}
	return filepath.Join(cp.Get().UserConfigDir(), defaultConfigFile)
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

func EnableNativeProvisioning() error {
	path, data, err := migrationLoadLauncherConfig()
	if err != nil {
		return err
	}
	config, err := parseLauncherConfig(path, data)
	if err != nil {
		return err
	}
	config.setBool([]string{"provision-host-config"}, true)
	updated, err := config.marshal()
	if err != nil {
		return err
	}
	return atomicWriteFn(path, updated, defaultConfigMode)
}

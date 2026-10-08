package configmigration

import (
	"encoding/json"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/agent"
)

const (
	yamlIndent                 = 2
	defaultAuthMode            = 0o600
	defaultConfigMode          = 0o600
	statusFileName             = "config-migration.yaml"
	homeConfigKey              = "home"
	extYAML                    = ".yaml"
	extYML                     = ".yml"
	manifestVersion            = 1
	migrationStateApplying     = "applying"
	migrationStateCompleted    = "completed"
	migrationStateDismissed    = "dismissed"
	migrationStateManual       = "manual"
	migrationStateLoginPending = "login-pending"
	claudeCodeAgentName        = "claude-code"
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
	marshalYAMLFn                     = marshalYAML
)

func migrationHomeMappings(
	a agent.Agent,
	spec agent.ConfigMigrationSpec,
	includeCredential bool,
	supplementalNames []string,
) map[string]string {
	mappings := make(map[string]string)
	if includeCredential {
		mappings[spec.CredentialTarget] = filepath.Join(a.ConfigDirName(), spec.ManagedCredential)
	}
	for _, name := range supplementalNames {
		mappings[filepath.Join(filepath.Dir(spec.CredentialTarget), name)] = filepath.Join(a.ConfigDirName(), name)
	}
	return mappings
}

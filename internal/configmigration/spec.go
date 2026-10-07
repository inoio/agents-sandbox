package configmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func defaultMigrationSpec() agent.ConfigMigrationSpec {
	spec, ok := migrationSpecProviderFn()
	if ok {
		return spec
	}
	return agent.ConfigMigrationSpec{Auth: agent.AuthMigrationSpec{ //nolint:exhaustruct // test fallback
		SecretPrefix:            "OPENCODE",
		AuthPlaceholderPrefix:   "$MSB_",
		ConfigPlaceholderPrefix: "{env:",
	}}
}

//nolint:gochecknoglobals // package-local test seam for unsupported registries
var migrationSpecProviderFn = func() (agent.ConfigMigrationSpec, bool) {
	return lookupMigrationSpec(agent.Lookup)
}

func lookupMigrationSpec(lookup func(string) (agent.Agent, bool)) (agent.ConfigMigrationSpec, bool) {
	a, ok := lookup("opencode")
	if !ok {
		return agent.ConfigMigrationSpec{}, false
	}
	provider, ok := agent.AsMigrationSpecProvider(a)
	if !ok {
		return agent.ConfigMigrationSpec{}, false
	}
	return provider.MigrationSpec(), true
}

func shortHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])[:8]
}

func cloneSecrets(input map[string]Secret) map[string]Secret {
	result := make(map[string]Secret, len(input))
	for name, secret := range input {
		secret.Hosts = append([]string(nil), secret.Hosts...)
		result[name] = secret
	}
	return result
}

func buildLauncherConfig(a agent.Agent, spec agent.ConfigMigrationSpec) (string, []byte, error) {
	path, data, err := migrationLoadLauncherConfig()
	if err != nil {
		return "", nil, err
	}
	config, err := parseLauncherConfig(path, data)
	if err != nil {
		return "", nil, err
	}
	mappings := migrationHomeMappings(
		a,
		spec,
		spec.CredentialTarget != "" && spec.ManagedCredential != "",
		spec.ManagedSupplementalFiles,
	)
	changed := false
	for _, target := range slices.Sorted(maps.Keys(mappings)) {
		source := mappings[target]
		if existing, ok := migrationHomeMapping(config.values, target); ok {
			if existing != source {
				return "", nil, fmt.Errorf("home mapping %q already points to %q", target, existing)
			}
			continue
		}
		config.setString([]string{homeConfigKey, target}, source)
		changed = true
	}
	if !changed {
		return path, nil, nil
	}
	updated, err := config.marshal()
	if err != nil {
		return "", nil, err
	}
	return path, updated, nil
}

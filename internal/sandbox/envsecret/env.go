// Package envsecret loads the host environment variable files and secret-spec
// files that configure a project VM.
package envsecret

import (
	"errors"
	"maps"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// EnvKeyValueParts is the number of parts strings.SplitN should produce for
// key=value lines.
const EnvKeyValueParts = 2

// parseKeyValueLines splits data into trimmed, non-comment "key=value" lines
// and hands each split pair to onLine. Blank lines and lines without an '='
// separator are skipped by the split-count check below. The key and value are
// passed exactly as SplitN produced them (not re-trimmed); callers that need
// trimmed keys trim them in their post-processing.
func parseKeyValueLines(data string, onLine func(key, value string) error) error {
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", EnvKeyValueParts)
		if len(parts) != EnvKeyValueParts {
			continue
		}
		if err := onLine(parts[0], parts[1]); err != nil {
			return err
		}
	}
	return nil
}

// BuildEnvMap reads environment variables from a file. A line without a
// variable name is skipped with a warning, because the VM refuses to boot with
// an empty env name.
func BuildEnvMap(filename string, ui termio.Logger) map[string]string {
	data, err := projectguard.ReadFile(filename, configread.ReadFile)
	if err != nil {
		if errors.Is(err, configread.ErrTooLarge) || errors.Is(err, projectguard.ErrNotApproved) {
			ui.Warnf("ignoring env file %s: %v", filename, err)
		}
		return nil
	}
	return ParseEnv(filename, data, ui)
}

// ParseEnv parses the env file data read from filename. A line without a
// variable name is skipped with a warning.
func ParseEnv(filename string, data []byte, ui termio.Logger) map[string]string {
	env := make(map[string]string)
	_ = parseKeyValueLines(string(data), func(key, value string) error {
		if key == "" {
			ui.Warnf("ignoring an env entry without a variable name in %s", filename)
			return nil
		}
		env[key] = value
		return nil
	})
	return env
}

// MergeEnvMaps merges multiple environment maps into one.
func MergeEnvMaps(mapsToMerge ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range mapsToMerge {
		maps.Copy(result, m)
	}
	return result
}

// EnvSecrets is the project VM's desired host configuration: the merged env
// vars and the built secret entries read from the host env and secret files. A
// run loads it once and threads the one value to the config fingerprints, the
// reconfig change detection, and VM creation, so those files are read (and
// warned about) once per run.
//
//exhaustruct:ignore
type EnvSecrets struct {
	Vars    map[string]string
	Secrets []msbSdk.SecretEntry
}

// LoadEnvAndSecrets reads every config scope's env and secret-spec files and
// merges them into the desired env and secrets. Scopes are ordered lowest
// precedence first, so later scopes override earlier ones. Secret specs are
// format-tiered: every scope's legacy specs merge before any scope's YAML specs,
// so a YAML entry beats a legacy entry for the same key even across scopes.
// allowPlaceholderFor lists hosts that may receive any secret's placeholder
// unchanged (the secrets.common.allow-placeholder-for config key); each secret
// still substitutes for the hosts it allows.
func LoadEnvAndSecrets(ui termio.Logger, scopes []cp.ConfigScope, allowPlaceholderFor []string) EnvSecrets {
	var envMaps []map[string]string
	for _, scope := range scopes {
		envMaps = append(envMaps, BuildEnvMap(scope.EnvFile(), ui))
	}

	var specs []map[string]SecretSpec
	for _, scope := range scopes {
		specs = append(specs, ParseSecretSpecLegacy(scope.EnvSecretFile(), ui))
	}
	for _, scope := range scopes {
		specs = append(specs, ParseSecretSpecYAML(scope.EnvSecretYAMLFile(), ui))
	}

	return EnvSecrets{
		Vars:    MergeEnvMaps(envMaps...),
		Secrets: BuildSecretsFromSpecs(MergeSecretSpecs(specs...), allowPlaceholderFor, ui),
	}
}

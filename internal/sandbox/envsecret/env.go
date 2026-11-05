// Package envsecret loads the host environment variable files and secret-spec
// files that configure a project VM.
package envsecret

import (
	"maps"
	"os"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	cp "github.com/inoio/agents-sandbox/internal/configpaths"
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
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
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

// LoadEnvAndSecrets reads the user and project env files and secret-spec files
// and merges them into the desired env and secrets. The project files override
// the user files. allowPlaceholderFor lists hosts that may receive any secret's
// placeholder unchanged (the secrets.common.allow-placeholder-for config key);
// each secret still substitutes for the hosts it allows.
func LoadEnvAndSecrets(ui termio.Logger, allowPlaceholderFor []string) EnvSecrets {
	return EnvSecrets{
		Vars: MergeEnvMaps(
			BuildEnvMap(cp.Get().UserEnvFile(), ui),
			BuildEnvMap(cp.Get().ProjectEnvFile(), ui),
		),
		Secrets: BuildSecretsFromSpecs(MergeSecretSpecs(
			ParseSecretSpecLegacy(cp.Get().UserEnvSecretFile(), ui),
			ParseSecretSpecLegacy(cp.Get().ProjectEnvSecretFile(), ui),
			ParseSecretSpecYAML(cp.Get().UserEnvSecretYAMLFile(), ui),
			ParseSecretSpecYAML(cp.Get().ProjectEnvSecretYAMLFile(), ui),
		), allowPlaceholderFor, ui),
	}
}

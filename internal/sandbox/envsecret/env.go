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

// parseKeyValueLines splits data into trimmed, non-blank, non-comment
// "key=value" lines and hands each split pair to onLine. The key and value are
// passed exactly as SplitN produced them (not re-trimmed); callers that need
// trimmed keys trim them in their post-processing.
func parseKeyValueLines(data string, onLine func(key, value string) error) error {
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
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

// BuildEnvMap reads environment variables from a file.
func BuildEnvMap(filename string) map[string]string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	env := make(map[string]string)
	_ = parseKeyValueLines(string(data), func(key, value string) error {
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

// LoadEnvAndSecrets reads the user and project env files and secret-spec files
// and merges them into the desired env map and built secret entries. The
// project files override the user files.
func LoadEnvAndSecrets(ui termio.UI) (map[string]string, []msbSdk.SecretEntry) {
	env := MergeEnvMaps(
		BuildEnvMap(cp.Get().UserEnvFile()),
		BuildEnvMap(cp.Get().ProjectEnvFile()),
	)
	secrets := BuildSecretsFromSpecs(MergeSecretSpecs(
		ParseSecretSpecLegacy(cp.Get().UserEnvSecretFile(), ui),
		ParseSecretSpecLegacy(cp.Get().ProjectEnvSecretFile(), ui),
		ParseSecretSpecYAML(cp.Get().UserEnvSecretYAMLFile(), ui),
		ParseSecretSpecYAML(cp.Get().ProjectEnvSecretYAMLFile(), ui),
	), ui)
	return env, secrets
}

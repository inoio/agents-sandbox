package journeys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// ScopeWant is the semantic effect StepAssertScopeWritten checks in the per-slug
// user scope: the credential's env-var name, the hosts it is scoped to, the
// config keys the managed config must carry, and whether the managed config
// references the credential as the ${ENVVAR} placeholder. It is deliberately
// semantic, never a full-file golden.
type ScopeWant struct {
	EnvVar      string
	Hosts       []string
	ConfigKeys  []string
	Placeholder bool
}

// StepAssertScopeWritten asserts want's semantic effects in the per-slug user
// scope the run wrote: the credential's env-var name and hosts in
// env.secret.yaml, and the expected config keys with the ${ENVVAR} placeholder
// in the managed agent config.
func StepAssertScopeWritten(want ScopeWant) Step {
	return AsCheck(Step{Name: "the per-slug scope holds the learned credential", Run: func(c *Context) error {
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		if err := assertSecretScope(dir, want); err != nil {
			return err
		}
		return assertManagedConfig(dir, c.Agent, want)
	}})
}

// StepAssertScopeExcludesHostLoginStore asserts the harness's host login store
// (Harness.HostLoginStore) is not present in the per-slug scope's managed agent
// config dir. A login credential store such as opencode's auth.json must never
// be copied into the scope by the host seed, and its absence is not covered by
// the key/placeholder checks.
func StepAssertScopeExcludesHostLoginStore() Step {
	return AsCheck(Step{Name: "the per-slug scope excludes the host login store", Run: func(c *Context) error {
		if len(c.Harness.HostLoginStore) == 0 {
			return fmt.Errorf("the %s adapter declares no host login store", c.Agent)
		}
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		profile, ok := agent.Lookup(c.Agent)
		if !ok {
			return fmt.Errorf("unknown agent %q", c.Agent)
		}
		configDirNames, err := agent.ConfigDirNames(profile)
		if err != nil {
			return fmt.Errorf("agent %s config-dir names: %w", c.Agent, err)
		}
		scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
		configDir := scope.AgentConfigDir(configDirNames[0])
		for relPath := range c.Harness.HostLoginStore {
			path := filepath.Join(configDir, filepath.Base(relPath))
			_, err := os.Stat(path)
			if err == nil {
				return fmt.Errorf("the per-slug scope copied %s, which the host seed must drop", path)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat %s: %w", path, err)
			}
		}
		return nil
	}})
}

// assertSecretScope checks env.secret.yaml in dir for the wanted env-var name
// and host scoping.
func assertSecretScope(dir string, want ScopeWant) error {
	if want.EnvVar == "" && len(want.Hosts) == 0 {
		return nil
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
	path := scope.EnvSecretYAMLFile()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	text := string(data)
	if want.EnvVar != "" && !strings.Contains(text, want.EnvVar) {
		return fmt.Errorf("env.secret.yaml does not declare %s:\n%s", want.EnvVar, text)
	}
	for _, host := range want.Hosts {
		if !strings.Contains(text, host) {
			return fmt.Errorf("env.secret.yaml does not scope %s to host %q:\n%s", want.EnvVar, host, text)
		}
	}
	return nil
}

// assertManagedConfig checks the managed agent config in dir for the wanted
// config keys and the ${ENVVAR} placeholder.
func assertManagedConfig(dir, agentName string, want ScopeWant) error {
	if len(want.ConfigKeys) == 0 && !want.Placeholder {
		return nil
	}
	profile, ok := agent.Lookup(agentName)
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	names, err := agent.ConfigDirNames(profile)
	if err != nil {
		return fmt.Errorf("agent %s config-dir names: %w", agentName, err)
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
	configDir := scope.AgentConfigDir(names[0])
	text, err := readRegularFiles(configDir)
	if err != nil {
		return err
	}
	for _, key := range want.ConfigKeys {
		if !strings.Contains(text, key) {
			return fmt.Errorf("the managed agent config in %s does not carry key %q:\n%s", configDir, key, text)
		}
	}
	if want.Placeholder {
		if want.EnvVar == "" {
			return errors.New("a placeholder assertion needs the credential's env-var name")
		}
		placeholder := "${" + want.EnvVar + "}"
		if !strings.Contains(text, placeholder) {
			return fmt.Errorf("the managed agent config in %s does not carry %s:\n%s", configDir, placeholder, text)
		}
	}
	return nil
}

// readRegularFiles concatenates every regular file directly under dir. A
// missing directory reports the empty string, so absence of a managed config is
// reported by the key/placeholder assertions rather than as an I/O error.
func readRegularFiles(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}
	var builder strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		builder.Write(data)
	}
	return builder.String(), nil
}

package journeys

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// ScopeWant is the evidence a journey expects in the per-slug user scope beyond
// the scoped credential, which every learn journey asserts: the harness's
// managed-config keys (LearnFacts.ManagedConfigKeys), and the credential as the
// ${ENVVAR} placeholder in the managed config. What the keys and the env var are
// is the adapter's; whether the journey's path produces them is the journey's.
type ScopeWant struct {
	ManagedConfig bool
	Placeholder   bool
}

// scopeExpectation is a ScopeWant resolved against a harness's learn facts: the
// credential's env-var name, the hosts it is scoped to, the config keys the
// managed config must carry, and whether it carries the placeholder. It is
// deliberately semantic, never a full-file golden.
type scopeExpectation struct {
	envVar      string
	hosts       []string
	configKeys  []string
	placeholder bool
}

// expectation resolves want against learn for the stub's advertised host: the
// placeholder only where the harness's managed config can hold the credential.
func (want ScopeWant) expectation(learn LearnFacts) scopeExpectation {
	var configKeys []string
	if want.ManagedConfig {
		configKeys = learn.ManagedConfigKeys
	}
	return scopeExpectation{
		envVar:      learn.CredentialEnvVar,
		hosts:       []string{stubHost},
		configKeys:  configKeys,
		placeholder: want.Placeholder && learn.ManagedConfigHoldsCredential,
	}
}

// learnFacts returns the harness's learn facts, or an error naming a harness no
// learn-mode journey can cover yet.
func learnFacts(c *Context) (LearnFacts, error) {
	if c.Harness.Learn == nil {
		return LearnFacts{}, fmt.Errorf("the %s adapter declares no learn facts", c.Agent)
	}
	return *c.Harness.Learn, nil
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
		learn, err := learnFacts(c)
		if err != nil {
			return err
		}
		expectation := want.expectation(learn)
		if err := assertSecretScope(dir, expectation); err != nil {
			return err
		}
		return assertManagedConfig(dir, c.Agent, expectation)
	}})
}

// StepAssertScopeExcludesHostLoginStore asserts no file in the per-slug scope's
// managed agent config is the harness's host login store
// (Harness.Learn.HostLoginStore): none takes the store's file name, and none
// holds its content, which catches a copy under another name as long as the
// store holds no static value the host seed would rewrite. A login credential
// store such as opencode's auth.json must never be copied into the scope, and
// its absence is not covered by the key/placeholder checks.
func StepAssertScopeExcludesHostLoginStore() Step {
	return AsCheck(Step{Name: "the per-slug scope excludes the host login store", Run: func(c *Context) error {
		learn, err := learnFacts(c)
		if err != nil {
			return err
		}
		if len(learn.HostLoginStore) == 0 {
			return fmt.Errorf("the %s adapter declares no host login store", c.Agent)
		}
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		files, err := managedConfigFiles(dir, c.Agent)
		if err != nil {
			return err
		}
		for relPath, content := range learn.HostLoginStore {
			for path, data := range files {
				if filepath.Base(path) == filepath.Base(relPath) || strings.Contains(data, content) {
					return fmt.Errorf(
						"the per-slug scope copied the host login store %s as %s, which the host seed must drop",
						relPath,
						path,
					)
				}
			}
		}
		return nil
	}})
}

// assertSecretScope checks env.secret.yaml in dir for the wanted env-var name
// and host scoping.
func assertSecretScope(dir string, want scopeExpectation) error {
	if want.envVar == "" && len(want.hosts) == 0 {
		return nil
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
	path := scope.EnvSecretYAMLFile()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	text := string(data)
	if want.envVar != "" && !strings.Contains(text, want.envVar) {
		return fmt.Errorf("env.secret.yaml does not declare %s:\n%s", want.envVar, text)
	}
	for _, host := range want.hosts {
		if !strings.Contains(text, host) {
			return fmt.Errorf("env.secret.yaml does not scope %s to host %q:\n%s", want.envVar, host, text)
		}
	}
	return nil
}

// assertManagedConfig checks the managed agent config in dir for the wanted
// config keys and the ${ENVVAR} placeholder.
func assertManagedConfig(dir, agentName string, want scopeExpectation) error {
	if len(want.configKeys) == 0 && !want.placeholder {
		return nil
	}
	text, err := managedConfigText(dir, agentName)
	if err != nil {
		return err
	}
	for _, key := range want.configKeys {
		if !strings.Contains(text, key) {
			return fmt.Errorf("the managed agent config in %s does not carry key %q:\n%s", dir, key, text)
		}
	}
	if want.placeholder {
		if want.envVar == "" {
			return errors.New("a placeholder assertion needs the credential's env-var name")
		}
		placeholder := "${" + want.envVar + "}"
		if !strings.Contains(text, placeholder) {
			return fmt.Errorf("the managed agent config in %s does not carry %s:\n%s", dir, placeholder, text)
		}
	}
	return nil
}

// readConfigFiles adds every config file under dir, recursively, to files: a
// regular file or a symlink to one, skipping an installed package tree
// (node_modules), whose text is not config. A missing dir adds nothing, so the
// absence of a managed config is reported by the assertions, not as an I/O
// error.
func readConfigFiles(dir string, files map[string]string) error {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		files[path] = string(data)
		return nil
	})
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	return nil
}

// managedConfigText is the concatenated content of managedConfigFiles.
func managedConfigText(dir, agentName string) (string, error) {
	files, err := managedConfigFiles(dir, agentName)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, path := range slices.Sorted(maps.Keys(files)) {
		text.WriteString(files[path])
	}
	return text.String(), nil
}

// managedConfigFiles maps every config file under each of the agent's config
// dirs in the per-slug scope at dir to its content, so an assertion does not
// depend on which config dir the read-back or the host seed writes to.
func managedConfigFiles(dir, agentName string) (map[string]string, error) {
	profile, ok := agent.Lookup(agentName)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", agentName)
	}
	names, err := agent.ConfigDirNames(profile)
	if err != nil {
		return nil, fmt.Errorf("agent %s config-dir names: %w", agentName, err)
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
	files := map[string]string{}
	for _, name := range names {
		if err := readConfigFiles(scope.AgentConfigDir(name), files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

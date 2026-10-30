package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ConfigDirNames returns the agent's launcher config-dir names, primary first,
// then each alias. The caller joins them under the tool's user or project config
// dir; each name is validated as a single path element.
func ConfigDirNames(a Agent) ([]string, error) {
	names := []string{a.ConfigDirName()}
	if aliaser, ok := AsConfigDirAliaser(a); ok {
		names = append(names, aliaser.ConfigDirAliases()...)
	}
	for _, name := range names {
		if err := validateConfigDirName(a.Name(), name); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// validateConfigDirName rejects a name that is not a single clean path element.
// Without this, names like "", ".", ".." or "a/b" would resolve to the launcher
// config root or a parent: filepath.Join cleans "..", so the config mirror could
// copy unrelated host files — including launcher secrets — into the VM. A bad
// name is a profile programming error, so it is returned as an error rather than
// panicking deep inside a path join.
func validateConfigDirName(agentName, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) {
		return fmt.Errorf(
			"agent %s declares invalid config dir name %q: must be a single path element",
			agentName,
			name,
		)
	}
	return nil
}

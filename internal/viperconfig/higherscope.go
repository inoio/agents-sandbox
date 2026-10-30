package viperconfig

import (
	"fmt"
	"os"

	"github.com/titanous/json5"
	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/configpaths"

	"github.com/spf13/cobra"
)

// flagNetworkName is the CLI flag name whose values feed the nested
// network.profile key. It has no key-named flag, so higherScopeKeys treats it
// specially.
const flagNetworkName = "network"

// KeySetAboveUserConfig reports whether key is set at a scope more specific
// than the generic user config: the per-slug user config or project config
// file, a launcher env var, or an explicit command-line flag.
func (r *Resolver) KeySetAboveUserConfig(key string) bool {
	return r.higherScope[key]
}

// higherScopeKeys collects the config keys set above the generic user config
// for the given slug: the per-slug user config file, the project config file,
// any launcher env var, and any explicitly set config-backed flag on cmd.
func higherScopeKeys(cmd *cobra.Command, slug string) (map[string]bool, error) {
	keys := map[string]bool{}
	cp := configpaths.Get()
	if slug != "" {
		if err := addConfigFileKeys(keys, cp.UserSlugConfigDir(slug)); err != nil {
			return nil, err
		}
	}
	if err := addConfigFileKeys(keys, cp.ProjectConfigDir()); err != nil {
		return nil, err
	}
	for _, key := range configEnvKeys {
		if os.Getenv(envVarName(EnvPrefix, key)) != "" ||
			os.Getenv(envVarName(LegacyEnvPrefix, key)) != "" {
			keys[key] = true
		}
	}
	if cmd != nil {
		for _, key := range configBindKeys {
			if flag := findFlag(cmd, key); flag != nil && flag.Changed {
				keys[key] = true
			}
		}
		if flag := findFlag(cmd, flagNetworkName); flag != nil && flag.Changed {
			keys[keyNetworkProfile] = true
		}
	}
	return keys, nil
}

// addConfigFileKeys adds every leaf key in dir's config file, if any, to keys.
func addConfigFileKeys(keys map[string]bool, dir string) error {
	leafKeys, err := configFileLeafKeys(dir)
	if err != nil {
		return err
	}
	for _, key := range leafKeys {
		keys[key] = true
	}
	return nil
}

// configFileLeafKeys returns the dotted leaf keys declared in dir's config
// file, or nil when no config file exists there.
func configFileLeafKeys(dir string) ([]string, error) {
	path, ext, ok := configpaths.FindConfigFile(dir)
	if !ok {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read launcher config %s: %w", path, err)
	}
	var raw map[string]any
	if configType(ext) == ctYAML {
		if unmarshalErr := yaml.Unmarshal(data, &raw); unmarshalErr != nil {
			return nil, fmt.Errorf("parse launcher config %s: %w", path, unmarshalErr)
		}
	} else if unmarshalErr := json5.Unmarshal(data, &raw); unmarshalErr != nil {
		return nil, fmt.Errorf("parse launcher config %s: %w", path, unmarshalErr)
	}
	var keys []string
	collectLeafKeys(raw, "", &keys)
	return keys, nil
}

// collectLeafKeys appends every leaf key of node to keys, joining nesting with
// a dot so a nested network.profile key is reported as "network.profile".
func collectLeafKeys(node map[string]any, prefix string, keys *[]string) {
	for name, value := range node {
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		if child, ok := value.(map[string]any); ok {
			collectLeafKeys(child, key, keys)
			continue
		}
		*keys = append(*keys, key)
	}
}

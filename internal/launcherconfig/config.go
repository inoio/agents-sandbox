package launcherconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	json5 "github.com/titanous/json5"
)

// Config holds launcher-level defaults that can be set in
// ~/.config/opencode-msb/config.* and .opencode-msb/config.*.
type Config struct {
	Yes     bool   `mapstructure:"yes"`
	Verbose bool   `mapstructure:"verbose"`
	Quiet   bool   `mapstructure:"quiet"`
	CPUs    uint8  `mapstructure:"cpus"`
	Memory  string `mapstructure:"memory"`
	Rebuild bool   `mapstructure:"rebuild"`
}

const (
	extJSON5 = ".json5"
	extJSONC = ".jsonc"
)

//nolint:gochecknoglobals // package-level constant slice
var supportedExts = []string{".yaml", ".yml", ".json", extJSONC, extJSON5}

// Load reads launcher config files from userDir and projectDir. Missing files
// are ignored. Project values override user values. The returned map contains
// the top-level keys that were explicitly set in either file.
func Load(userDir, projectDir string) (Config, map[string]bool, error) {
	v := viper.New()
	if err := mergeDir(v, userDir); err != nil {
		return Config{}, nil, err
	}
	if err := mergeDir(v, projectDir); err != nil {
		return Config{}, nil, err
	}
	if err := validate(v); err != nil {
		return Config{}, nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, nil, fmt.Errorf("decode launcher config: %w", err)
	}
	keys := make(map[string]bool, len(v.AllSettings()))
	for k := range v.AllSettings() {
		keys[k] = true
	}
	return cfg, keys, nil
}

func mergeDir(v *viper.Viper, dir string) error {
	path, ext, ok := findConfigFile(dir)
	if !ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read launcher config %s: %w", path, err)
	}
	ct := configType(ext)
	if ext == extJSON5 || ext == extJSONC {
		var m map[string]any
		if unmarshalErr := json5.Unmarshal(data, &m); unmarshalErr != nil {
			return fmt.Errorf("parse launcher config %s: %w", path, unmarshalErr)
		}
		data, err = json.Marshal(m)
		if err != nil {
			return fmt.Errorf("normalize launcher config %s: %w", path, err)
		}
		ct = "json"
	}
	v.SetConfigType(ct)
	if err := v.MergeConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("load launcher config %s: %w", path, err)
	}
	return nil
}

func findConfigFile(dir string) (string, string, bool) {
	if dir == "" {
		return "", "", false
	}
	for _, ext := range supportedExts {
		path := filepath.Join(dir, "config"+ext)
		if _, err := os.Stat(path); err == nil {
			return path, ext, true
		}
	}
	return "", "", false
}

func configType(ext string) string {
	switch strings.ToLower(ext) {
	case ".yaml", ".yml":
		return "yaml"
	case ".json", ".jsonc", ".json5":
		return "json"
	}
	return ""
}

func validate(v *viper.Viper) error {
	if !v.IsSet("cpus") {
		return nil
	}
	cpus := v.GetInt("cpus")
	if cpus < 0 || cpus > 255 {
		return fmt.Errorf("launcher config cpus must be between 0 and 255, got %d", cpus)
	}
	return nil
}

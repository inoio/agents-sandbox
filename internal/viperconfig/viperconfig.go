package viperconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"github.com/titanous/json5"
)

// Config holds launcher-level defaults that can be set in
// ~/.config/opencode-msb/config.* and .opencode-msb/config.*.
type Config struct {
	AutoPruneAge   time.Duration `mapstructure:"auto-prune-age"`
	ManualPruneAge time.Duration `mapstructure:"manual-prune-age"`
	Memory         string        `mapstructure:"memory"`
	TmpSize        string        `mapstructure:"tmp-size"`
	DiskSize       string        `mapstructure:"disk-size"`
	Yes            bool          `mapstructure:"yes"`
	Verbose        bool          `mapstructure:"verbose"`
	Quiet          bool          `mapstructure:"quiet"`
	Rebuild        bool          `mapstructure:"rebuild"`
	CPUs           uint8         `mapstructure:"cpus"`

	AutoStopOnActiveSessions  bool          `mapstructure:"auto-stop-on-active-sessions"`
	AutoStopTimeout           time.Duration `mapstructure:"auto-stop-timeout"`
	AutoStopMaxSessionRetries int           `mapstructure:"auto-stop-max-session-retries"`
}

const (
	extJSON5                     = ".json5"
	extJSONC                     = ".jsonc"
	keyAutoPruneAge              = "auto-prune-age"
	keyManualPruneAge            = "manual-prune-age"
	keyAutoStopOnActiveSessions  = "auto-stop-on-active-sessions"
	keyAutoStopTimeout           = "auto-stop-timeout"
	keyAutoStopMaxSessionRetries = "auto-stop-max-session-retries"
)

//nolint:gochecknoglobals // package-level constant slice
var supportedExts = []string{".yaml", ".yml", ".json", extJSONC, extJSON5}

// ParseHumanDuration parses duration strings like "7d", "2w", "6h", "30m"
// into time.Duration. Go's time.ParseDuration supports ns/us/ms/s/m/h
// but not "d" (days) or "w" (weeks).
func ParseHumanDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasSuffix(s, "w"):
		num, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, false
		}
		return time.Duration(num) * 7 * 24 * time.Hour, true
	case strings.HasSuffix(s, "d"):
		num, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, false
		}
		return time.Duration(num) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, true
	}
	return 0, false
}

func durationDecodeHook() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}
		if t.Kind() != reflect.Interface && t != reflect.TypeFor[time.Duration]() {
			return data, nil
		}
		str, ok := data.(string)
		if !ok {
			return data, nil
		}
		if d, ok := ParseHumanDuration(str); ok {
			return d, nil
		}
		if d, err := time.ParseDuration(str); err == nil {
			return d, nil
		}
		return data, nil
	}
}

// Load reads launcher config files from userDir and projectDir. Missing files
// are ignored. Project values override user values. The returned map contains
// the top-level keys that were explicitly set in either file.
func Load() (Config, map[string]bool, error) {
	v := viper.New()

	if err := mergeDir(v, configpaths.GetConfigPaths().UserConfigDir()); err != nil {
		return Config{}, nil, err
	}
	if err := mergeDir(v, configpaths.GetConfigPaths().ProjectConfigDir()); err != nil {
		return Config{}, nil, err
	}
	if err := validate(v); err != nil {
		return Config{}, nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			durationDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
		),
	)); err != nil {
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
	// prune-age validation does not gate on cpus being set, so run it
	// before the cpus early-exit below.
	if err := validatePruneAges(v); err != nil {
		return err
	}
	if err := validateAutoStop(v); err != nil {
		return err
	}
	if err := validateAutoStopTimeout(v); err != nil {
		return err
	}
	if !v.IsSet("cpus") {
		return nil
	}
	cpus := v.GetInt("cpus")
	if cpus < 0 || cpus > 255 {
		return fmt.Errorf("launcher config cpus must be between 0 and 255, got %d", cpus)
	}
	return nil
}

func validatePruneAges(v *viper.Viper) error {
	for _, key := range []string{keyAutoPruneAge, keyManualPruneAge} {
		if !v.IsSet(key) {
			continue
		}
		d := v.GetDuration(key)
		if d > 0 {
			continue
		}
		// viper's GetDuration returned 0 — check if the raw value is a
		// "7d"-style string that the decode hook failed to convert.
		if s, ok := v.Get(key).(string); ok {
			if parsed, ok := ParseHumanDuration(s); ok {
				d = parsed
			}
		}
		if d <= 0 {
			return fmt.Errorf("launcher config %s must be > 0, got %v", key, d)
		}
	}
	return nil
}

func validateAutoStop(v *viper.Viper) error {
	if !v.IsSet(keyAutoStopMaxSessionRetries) {
		return nil
	}
	retries := v.GetInt(keyAutoStopMaxSessionRetries)
	if retries >= 0 {
		return nil
	}
	return fmt.Errorf("launcher config %s must be >= 0, got %d", keyAutoStopMaxSessionRetries, retries)
}

func validateAutoStopTimeout(v *viper.Viper) error {
	if !v.IsSet(keyAutoStopTimeout) {
		return nil
	}
	d := v.GetDuration(keyAutoStopTimeout)
	if d > 0 {
		return nil
	}
	if s, ok := v.Get(keyAutoStopTimeout).(string); ok {
		if parsed, ok := ParseHumanDuration(s); ok {
			d = parsed
		}
	}
	if d <= 0 {
		return fmt.Errorf("launcher config %s must be > 0, got %v", keyAutoStopTimeout, d)
	}
	return nil
}

func (c Config) IdleTimeout() time.Duration {
	if c.AutoStopTimeout > 0 {
		return c.AutoStopTimeout
	}
	return 10 * time.Second
}

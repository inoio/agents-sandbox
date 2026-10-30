package viperconfig

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-viper/mapstructure/v2"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// Per-slug mergeDir failure propagates out of NewResolver.
func TestNewResolverPerSlugMergeError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	slugDir := filepath.Join(cp.UserConfigDir(), "slug")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatalf("mkdir per-slug: %v", err)
	}
	testutil.WriteFile(t, slugDir, "config.yaml", "cpus: 2\n  memory: [\n")

	_, err := NewResolver(nil, "slug")
	if err == nil {
		t.Fatal("expected error for malformed per-slug config")
	}
}

// Project dir mergeDir failure propagates out of NewResolver.
func TestNewResolverProjectMergeError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "cpus: 2\n  memory: [\n")

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected error for malformed project config")
	}
}

// A config.yaml that is a directory makes os.ReadFile fail inside mergeDir.
func TestNewResolverUnreadableConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	if err := os.MkdirAll(filepath.Join(cp.UserConfigDir(), "config.yaml"), 0o755); err != nil {
		t.Fatalf("mkdir config.yaml: %v", err)
	}

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected error reading a directory as config")
	}
	if !strings.Contains(err.Error(), "read launcher config") {
		t.Errorf("error %q does not name the read failure", err)
	}
	if _, ok := errors.AsType[*os.PathError](err); !ok {
		t.Errorf("error %q does not wrap the underlying path error", err)
	}
}

// A malformed json5 config makes json5.Unmarshal fail inside mergeDir.
func TestNewResolverInvalidJSON5(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.json5", `{"cpus": }`)

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected error for malformed json5 config")
	}
	if !strings.Contains(err.Error(), "parse launcher config") {
		t.Errorf("error %q does not name the parse failure", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

// A json5 value that json.Marshal cannot serialize (NaN) makes the normalize
// step fail inside mergeDir.
func TestNewResolverJSON5MarshalError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.json5", `{"cpus": NaN}`)

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected error for non-serializable json5 config")
	}
	if !strings.Contains(err.Error(), "normalize launcher config") {
		t.Errorf("error %q does not name the normalize failure", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

// A plain .json config with invalid JSON triggers the non-yaml MergeConfig
// error path.
func TestNewResolverInvalidJSON(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.json", `{"cpus": }`)

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected error for malformed json config")
	}
	if !strings.Contains(err.Error(), "load launcher config") {
		t.Errorf("error %q does not name the load failure", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

// auto-stop-timeout of 0 makes validate() reject via validateAutoStopTimeout.
func TestNewResolverInvalidAutoStopTimeout(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"auto-stop-timeout": 0})

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected error for auto-stop-timeout 0")
	}
}

// A positive prune age passes validatePruneAges without error.
func TestNewResolverValidPruneAge(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"auto-prune-age": "7d"})

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.AutoPruneAge() != 7*24*time.Hour {
		t.Errorf("AutoPruneAge = %v, want 7d", r.AutoPruneAge())
	}
}

// A config value of the wrong shape for a typed field (a list for the string
// memory key) makes viper.Unmarshal fail, surfacing as a decode error.
func TestNewResolverSurfacesDecodeError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": []any{1, 2}})

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected a decode error for a list-valued memory key")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error = %v, want it to name the offending memory key", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error = %v, want it to wrap its cause", err)
	}
}

// configType returns "" for unsupported extensions.
func TestConfigTypeUnknown(t *testing.T) {
	if got := configType(".toml"); got != "" {
		t.Errorf("configType(.toml) = %q; want \"\"", got)
	}
	if got := configType(".JSON5"); got != "json" {
		t.Errorf("configType(.JSON5) = %q; want json", got)
	}
}

// durationDecodeHook with a string-kind (but not string) source returns the
// data unchanged because the type assertion to string fails.
func TestDurationDecodeHookNonStringData(t *testing.T) {
	hook := durationDecodeHook()
	durType := reflect.TypeFor[time.Duration]()

	type namedStr string
	exec := func(data any) (any, error) {
		return mapstructure.DecodeHookExec(
			hook, reflect.ValueOf(data), reflect.New(durType).Elem(),
		)
	}

	got, err := exec(namedStr("7d"))
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != namedStr("7d") {
		t.Errorf("hook(namedStr) = %#v, want unchanged", got)
	}
}

// durationDecodeHook with a non-string target (interface) that receives a
// string still routes through the parse helpers.
func TestDurationDecodeHookInterfaceTarget(t *testing.T) {
	hook := durationDecodeHook()
	strType := reflect.TypeFor[string]()
	ifaceType := reflect.TypeFor[any]()

	got, err := mapstructure.DecodeHookExec(
		hook, reflect.ValueOf("7d"), reflect.New(ifaceType).Elem(),
	)
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != 7*24*time.Hour {
		t.Errorf("hook(interface,7d) = %v, want 7d", got)
	}

	// A string that neither helper can parse is returned unchanged.
	got, err = mapstructure.DecodeHookExec(
		hook, reflect.ValueOf("nope"), reflect.New(strType).Elem(),
	)
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if got != "nope" {
		t.Errorf("hook(string,nope) = %v, want unchanged", got)
	}
}

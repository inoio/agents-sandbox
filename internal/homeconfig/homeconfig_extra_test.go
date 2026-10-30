package homeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestParseHomeSectionRejectsNonStringSource(t *testing.T) {
	data := []byte("home:\n  .x:\n    source: 42\n")
	if _, _, err := ParseHomeSection(data, "config.yaml"); err == nil {
		t.Fatal("expected error for a non-string source")
	}
}

func TestParseHomeSectionRejectsNonStringHook(t *testing.T) {
	data := []byte("home:\n  .x:\n    hook: 42\n")
	if _, _, err := ParseHomeSection(data, "config.yaml"); err == nil {
		t.Fatal("expected error for a non-string hook")
	}
}

func TestParseHomeSectionRejectsUnsupportedValueType(t *testing.T) {
	for _, body := range []string{".x: 42\n", ".x:\n  - a\n  - b\n"} {
		data := []byte("home:\n  " + strings.ReplaceAll(body, "\n", "\n  ") + "\n")
		if _, _, err := ParseHomeSection(data, "config.yaml"); err == nil {
			t.Errorf("expected an error for value body %q", body)
		}
	}
}

func TestParseHomeSectionNilEntryIsEmpty(t *testing.T) {
	data := []byte("home:\n  .x:\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if e, ok := m[".x"]; !ok || e != (Entry{}) {
		t.Errorf("expected empty Entry for nil value, got %v", m[".x"])
	}
}

func TestParseHomeSectionHomeNotAMap(t *testing.T) {
	data := []byte("home: 42\n")
	if _, _, err := ParseHomeSection(data, "config.yaml"); err == nil {
		t.Fatal("expected an error when home is not a map")
	}
}

func TestParseHomeSectionUnknownTopLevelKeyType(t *testing.T) {
	// A top-level target that is not a string still parses into the manifest
	// keyed by its string form; only the value type is validated.
	data := []byte("home:\n  42: value\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if _, ok := m["42"]; !ok {
		t.Errorf("expected target 42 to be present, got %v", m)
	}
}

func TestReadHomeFromConfigDir(t *testing.T) {
	dir := t.TempDir()
	writeHomeConfig(t, dir, ".gitconfig:\n")
	m, has, err := ReadHomeFromConfigDir(dir)
	if err != nil {
		t.Fatalf("ReadHomeFromConfigDir: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if e, ok := m[".gitconfig"]; !ok || e != (Entry{}) {
		t.Errorf("expected .gitconfig entry, got %v", m)
	}
}

func TestReadHomeFromConfigDirNoConfigFile(t *testing.T) {
	m, has, err := ReadHomeFromConfigDir(t.TempDir())
	if err != nil {
		t.Fatalf("ReadHomeFromConfigDir: %v", err)
	}
	if has {
		t.Error("expected has=false when no config file exists")
	}
	if len(m) != 0 {
		t.Errorf("expected empty manifest, got %v", m)
	}
}

func TestReadHomeFromConfigDirConfigWithoutHomeKey(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.yaml", "cpus: 2\n")
	m, has, err := ReadHomeFromConfigDir(dir)
	if err != nil {
		t.Fatalf("ReadHomeFromConfigDir: %v", err)
	}
	if has {
		t.Error("expected has=false when config has no home key")
	}
	if len(m) != 0 {
		t.Errorf("expected empty manifest, got %v", m)
	}
}

func TestReadHomeFromConfigDirJSON(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.json", `{"home":{".gitconfig":""}}`)
	m, has, err := ReadHomeFromConfigDir(dir)
	if err != nil {
		t.Fatalf("ReadHomeFromConfigDir: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if e, ok := m[".gitconfig"]; !ok || e != (Entry{}) {
		t.Errorf("expected .gitconfig entry, got %v", m)
	}
}

func TestParseHomeSectionJSON(t *testing.T) {
	data := []byte(`{"home": {".gitconfig": "", ".config/tool/cfg.toml": "./tool/cfg.toml"}}`)
	m, has, err := ParseHomeSection(data, "config.json")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := Manifest{
		".gitconfig":            Entry{Source: ""},
		".config/tool/cfg.toml": Entry{Source: "./tool/cfg.toml"},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func TestReadHomeFromConfigDirReadError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadHomeFromConfigDir(dir); err == nil {
		t.Fatal("expected an error reading a config file that is a directory")
	}
}

func TestReadHomeFromConfigDirJSON5(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.json5", `{
		// json5 comment
		"home": {".gitconfig": ""},
		"cpus": 2,
	}`)
	m, has, err := ReadHomeFromConfigDir(dir)
	if err != nil {
		t.Fatalf("ReadHomeFromConfigDir: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if e, ok := m[".gitconfig"]; !ok || e != (Entry{}) {
		t.Errorf("expected .gitconfig entry, got %v", m)
	}
}

func TestReadHomeFromConfigDirJSON5ParseError(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.json5", `{"cpus": }`)
	if _, _, err := ReadHomeFromConfigDir(dir); err == nil {
		t.Fatal("expected an error for malformed json5")
	}
}

func TestReadHomeFromConfigDirJSON5NormalizeError(t *testing.T) {
	dir := t.TempDir()
	// NaN is valid json5 but cannot be re-serialized with encoding/json.
	testutil.WriteFile(t, dir, "config.json5", `{"home": {}, "cpus": NaN}`)
	if _, _, err := ReadHomeFromConfigDir(dir); err == nil {
		t.Fatal("expected an error normalizing non-serializable json5")
	}
}

func TestBuildHomeFilesNoManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	files, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if has {
		t.Error("expected has=false when no manifest exists")
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %v", files)
	}
	if len(missing) != 0 {
		t.Errorf("expected no missing sources, got %v", missing)
	}
}

func TestBuildHomeFilesRejectsInvalidUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n  source: [\n")
	if _, _, _, err := BuildHomeFilesWithModes(user, proj, vmHome, nil); err == nil {
		t.Fatal("expected an error for an invalid user manifest")
	}
}

func TestLoadLayersRejectsInvalidUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n  source: [\n")
	if _, _, err := LoadLayers([]string{user, proj}); err == nil {
		t.Fatal("expected an error for an invalid user manifest")
	}
}

func TestBuildHooksRejectsInvalidUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n  source: [\n")
	if _, err := BuildHooks(user, proj, vmHome, nil); err == nil {
		t.Fatal("expected an error for an invalid user manifest")
	}
}

func TestBuildHooksRejectsReservedTarget(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "s.sh"), "#!/bin/sh\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: s.sh\n  hook: startup\n")
	if _, err := BuildHooks(user, proj, vmHome, []string{".vpn/connect.sh"}); err == nil {
		t.Fatal("expected an error for a reserved hook target")
	}
}

func TestHasShebangMissingFile(t *testing.T) {
	if got := hasShebang(filepath.Join(t.TempDir(), "nope.sh")); got {
		t.Errorf("expected false for missing file, got true")
	}
}

func TestHasShebangWithoutTrailingNewline(t *testing.T) {
	// A shebang without a trailing newline is still detected.
	p := filepath.Join(t.TempDir(), "s.sh")
	testutil.WritePath(t, p, "#!/bin/bash")
	if !hasShebang(p) {
		t.Errorf("expected a shebang for %q", p)
	}
}

func TestHasShebangBareMarker(t *testing.T) {
	// A shebang with only whitespace after the marker is still a shebang.
	p := filepath.Join(t.TempDir(), "s.sh")
	testutil.WritePath(t, p, "#! \n")
	if !hasShebang(p) {
		t.Errorf("expected a shebang for a bare marker")
	}
}

func TestBuildHomeFilesReadsUserManifest(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WriteFile(t, home, ".gitconfig", "user=x\n")
	writeHomeConfig(t, user, ".gitconfig:\n")
	files, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if len(missing) != 0 {
		t.Errorf("expected no missing sources, got %v", missing)
	}
	want := "/home/dev/.gitconfig"
	if _, ok := files[want]; !ok {
		t.Errorf("expected file at %s, got %v", want, files)
	}
}

func TestErrorMessagesMentionRelevantTarget(t *testing.T) {
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{
			"empty target",
			func() error {
				_, err := ResolveVMTarget(vmHome, "", nil)
				return err
			},
			"must not be empty",
		},
		{
			"absolute target",
			func() error {
				_, err := ResolveVMTarget(vmHome, "/etc/passwd", nil)
				return err
			},
			"must be relative",
		},
		{
			"traversal",
			func() error {
				_, err := ResolveVMTarget(vmHome, "../evil", nil)
				return err
			},
			"escapes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestResolveVMTargetRootBaseFailsContainment pins the final containment guard:
// with a root home base the separator-prefixed comparison rejects the joined
// path instead of returning it.
func TestResolveVMTargetRootBaseFailsContainment(t *testing.T) {
	if _, err := ResolveVMTarget("/", "etc", nil); err == nil {
		t.Error("a root home base must fail the containment guard rather than return a path")
	}
}

func TestBuildHomeFilesReportsLoadErrorWithHasFalse(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(user, "config.yaml"), "home: [\n")

	files, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err == nil {
		t.Fatal("expected a load error from an invalid user config")
	}
	if files != nil || missing != nil || has {
		t.Errorf("on error want (nil, nil, false), got files=%v missing=%v has=%v", files, missing, has)
	}
}

func TestBuildHomeFilesContinuesPastMissingSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "present.txt"), "data")
	// The missing source sorts first, so it must not stop the later present one.
	writeHomeConfig(t, proj, ""+
		".aaa.txt: ./absent.txt\n"+
		".zzz.txt: ./present.txt\n")

	files, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has {
		t.Fatal("expected has=true for a present manifest")
	}
	if len(missing) != 1 || !strings.HasSuffix(missing[0], "absent.txt") {
		t.Errorf("missing = %v, want the absent source", missing)
	}
	if _, ok := files[filepath.Join(vmHome, ".zzz.txt")]; !ok {
		t.Errorf("the present source after a missing one must still be built, got %v", files)
	}
}

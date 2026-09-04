package homeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

func TestFindConfigFileEmptyDir(t *testing.T) {
	if _, _, ok := findConfigFile(""); ok {
		t.Error("expected ok=false for an empty dir")
	}
}

func TestFindConfigFileFirstExtWins(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.yaml", "a: 1\n")
	testutil.WriteFile(t, dir, "config.json", `{"a": 1}`)
	path, ext, ok := findConfigFile(dir)
	if !ok || filepath.Base(path) != "config.yaml" || ext != ".yaml" {
		t.Errorf("expected config.yaml to win, got path=%q ext=%q ok=%v", path, ext, ok)
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
	files, missing, has, err := BuildHomeFiles(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFiles: %v", err)
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
	if _, _, _, err := BuildHomeFiles(user, proj, vmHome, nil); err == nil {
		t.Fatal("expected an error for an invalid user manifest")
	}
}

func TestDescribeManifestRejectsInvalidUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n  source: [\n")
	if _, _, err := DescribeManifest(user, proj, vmHome, nil); err == nil {
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

func TestShebangInterpreterMissingFile(t *testing.T) {
	if got := shebangInterpreter(filepath.Join(t.TempDir(), "nope.sh")); got != "" {
		t.Errorf("expected empty interpreter for missing file, got %q", got)
	}
}

func TestShebangInterpreterWithoutTrailingNewline(t *testing.T) {
	// Reading runs to EOF (err) rather than a newline; the interpreter is still captured.
	p := filepath.Join(t.TempDir(), "s.sh")
	testutil.WritePath(t, p, "#!/bin/bash")
	if got := shebangInterpreter(p); got != "/bin/bash" {
		t.Errorf("shebangInterpreter = %q, want %q", got, "/bin/bash")
	}
}

func TestShebangInterpreterBareMarker(t *testing.T) {
	// A shebang with only whitespace after the marker leaves an empty
	// interpreter after trimming.
	p := filepath.Join(t.TempDir(), "s.sh")
	testutil.WritePath(t, p, "#! \n")
	if got := shebangInterpreter(p); got != "" {
		t.Errorf("expected empty interpreter for bare marker, got %q", got)
	}
}

func TestBuildHomeFilesReadsUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", user)
	testutil.WriteFile(t, user, ".gitconfig", "user=x\n")
	writeHomeConfig(t, user, ".gitconfig:\n")
	files, missing, has, err := BuildHomeFiles(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFiles: %v", err)
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

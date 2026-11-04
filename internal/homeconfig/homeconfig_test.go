package homeconfig

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/titanous/json5"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// FuzzParseHomeSection drives arbitrary config-file bytes (YAML/JSON) through
// the user-controlled "home:" manifest parser. It must never panic on hostile
// or malformed input (a crash here breaks `run` for a user with a bad config),
// and it must never report an error alongside a manifest.
func FuzzParseHomeSection(f *testing.F) {
	f.Add([]byte("home:\n  ~/.gitconfig: /host/gitconfig\n"))
	f.Add([]byte("home:\n  ~/.ssh: {source: /host/ssh, hook: copy}\n"))
	f.Add([]byte("home: {}\n"))
	f.Add([]byte("home: ~/.x\n"))
	f.Add([]byte("home: []\n"))
	f.Add([]byte("home:\n  1: /host\n"))
	f.Add([]byte("{}"))
	f.Add([]byte(""))
	f.Add([]byte("not: valid: yaml: ["))
	f.Add([]byte("home:\n\tx: y\n"))
	f.Add([]byte("home:\n  a: !!binary aGk=\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		manifest, _, err := ParseHomeSection(data, "fuzz.yaml")
		if err != nil && manifest != nil {
			t.Fatalf("ParseHomeSection(%q) returned an error and a non-nil manifest", data)
		}
	})
}

func TestParseHomeSectionErrorKeepsPresentFalse(t *testing.T) {
	if _, has, err := ParseHomeSection([]byte("a: ["), "config.yaml"); err == nil || has {
		t.Errorf("unmarshal error: has=%v err=%v, want has=false with an error", has, err)
	}
	if m, has, err := ParseHomeSection([]byte("home: 42\n"), "config.yaml"); err == nil || has || m != nil {
		t.Errorf("home-not-a-map: has=%v m=%v err=%v, want has=false m=nil with an error", has, m, err)
	}
}

func TestParseHomeSectionNoHomeKeyNonNilManifest(t *testing.T) {
	m, has, err := ParseHomeSection([]byte("cpus: 2\n"), "config.yaml")
	if err != nil || has {
		t.Fatalf("ParseHomeSection: has=%v err=%v", has, err)
	}
	if m == nil {
		t.Error("expected a non-nil empty manifest")
	}
}

func TestParseHomeSectionNilHomeNonNilManifest(t *testing.T) {
	m, has, err := ParseHomeSection([]byte("home:\n"), "config.yaml")
	if err != nil || !has {
		t.Fatalf("ParseHomeSection: has=%v err=%v", has, err)
	}
	if m == nil {
		t.Error("expected a non-nil manifest for a nil home value")
	}
}

// TestParseHomeSectionRejectsInvalidEntries pins the manifest's shape rules:
// home must be a map, an entry is a source string or a map whose source and
// hook are strings, the only hook is "startup" and root is a boolean. Each
// refusal names the file and the entry.
func TestParseHomeSectionRejectsInvalidEntries(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"home not a map", "home: 42\n", `parse config.yaml: home must be a map of target to source`},
		{
			"non-string source",
			"home:\n  .x:\n    source: 42\n",
			`parse config.yaml entry ".x": source must be a string`,
		},
		{"non-string hook", "home:\n  .x:\n    hook: 42\n", `parse config.yaml entry ".x": hook must be a string`},
		{
			"unknown hook",
			"home:\n  .x:\n    source: x\n    hook: boot\n",
			`entry ".x": hook must be "startup", got "boot"`,
		},
		{
			"non-boolean root",
			"home:\n  .x:\n    source: x\n    hook: startup\n    root: yes\n",
			`entry ".x": root must be a boolean`,
		},
		{"scalar value", "home:\n  .x: 42\n", `entry ".x": unsupported value type int`},
		{"list value", "home:\n  .x:\n    - a\n    - b\n", `entry ".x": unsupported value type []interface {}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseHomeSection([]byte(tc.data), "config.yaml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseHomeSection error = %v, want %q", err, tc.want)
			}
		})
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

func TestParseHomeSection(t *testing.T) {
	data := []byte("home:\n  .gitconfig:\n  .config/tool/cfg.toml: ./tool/cfg.toml\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
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

func TestParseHomeSectionNoHomeKey(t *testing.T) {
	data := []byte("cpus: 2\nmemory: 4G\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if has {
		t.Error("expected has=false when no home key")
	}
	if len(m) != 0 {
		t.Errorf("expected empty manifest, got %v", m)
	}
}

func TestParseHomeSectionEmptyHomeKey(t *testing.T) {
	data := []byte("home:\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if !has {
		t.Error("expected has=true for an empty home key")
	}
	if len(m) != 0 {
		t.Errorf("expected empty manifest, got %v", m)
	}
}

func TestParseHomeSectionStructuredEntry(t *testing.T) {
	data := []byte("home:\n  .vpn/connect.sh:\n    source: vpn/connect.sh\n    hook: startup\n    root: true\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := Manifest{
		".vpn/connect.sh": Entry{Source: "vpn/connect.sh", Hook: "startup", Root: true},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func TestParseHomeSectionSyntaxErrorIsFriendly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("home:\n  .gitconfig:\n    source: [\n")
	_, _, err := ParseHomeSection(data, path)
	if err == nil {
		t.Fatal("expected error for malformed home section")
	}
	for _, want := range []string{path, "invalid YAML", "line"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestReadHomeFromConfigDirNoConfigNonNilManifest pins that a directory
// without a config file has no home section and an empty, non-nil manifest.
func TestReadHomeFromConfigDirNoConfigNonNilManifest(t *testing.T) {
	m, has, err := ReadHomeFromConfigDir(t.TempDir())
	if err != nil || has {
		t.Fatalf("ReadHomeFromConfigDir: has=%v err=%v", has, err)
	}
	if m == nil {
		t.Error("expected a non-nil empty manifest")
	}
}

func TestReadHomeFromConfigDirReadErrorKeepsPresentFalse(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, has, err := ReadHomeFromConfigDir(dir)
	if err == nil || has {
		t.Fatalf("read error: has=%v err=%v, want has=false with an error", has, err)
	}
	if _, ok := errors.AsType[*fs.PathError](err); !ok {
		t.Errorf("expected the wrapped *fs.PathError, got %v", err)
	}
}

// TestReadHomeFromConfigDirReadsEveryFormat pins that the home section is read
// from a YAML, JSON, JSON5 or JSONC config, comments and trailing commas
// included.
func TestReadHomeFromConfigDirReadsEveryFormat(t *testing.T) {
	cases := []struct {
		file    string
		content string
	}{
		{"config.yaml", "home:\n  .gitconfig:\n"},
		{"config.json", `{"home":{".gitconfig":""}}`},
		{"config.json5", "{\n\t// json5 comment\n\t\"home\": {\".gitconfig\": \"\"},\n\t\"cpus\": 2,\n}"},
		{"config.jsonc", "{\n\t// a comment line\n\t\"home\": {\".gitconfig\": \"\"},\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFile(t, dir, tc.file, tc.content)
			m, has, err := ReadHomeFromConfigDir(dir)
			if err != nil || !has {
				t.Fatalf("ReadHomeFromConfigDir: has=%v err=%v", has, err)
			}
			if e, ok := m[".gitconfig"]; !ok || e != (Entry{}) {
				t.Errorf("manifest = %v, want an empty .gitconfig entry", m)
			}
		})
	}
}

func TestReadHomeFromConfigDirJSON5ParseErrorKeepsPresentFalse(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.json5", `{"cpus": }`)
	_, has, err := ReadHomeFromConfigDir(dir)
	if err == nil || has {
		t.Fatalf("json5 parse error: has=%v err=%v", has, err)
	}
	if _, ok := errors.AsType[*json5.SyntaxError](err); !ok {
		t.Errorf("expected the wrapped *json5.SyntaxError, got %v", err)
	}
}

func TestReadHomeFromConfigDirNormalizeErrorKeepsPresentFalse(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.json5", `{"home": {}, "cpus": NaN}`)
	_, has, err := ReadHomeFromConfigDir(dir)
	if err == nil || has {
		t.Fatalf("normalize error: has=%v err=%v", has, err)
	}
	if _, ok := errors.AsType[*json.UnsupportedValueError](err); !ok {
		t.Errorf("expected the wrapped *json.UnsupportedValueError, got %v", err)
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

func TestParseEntrySuccessPaths(t *testing.T) {
	if e, err := parseEntry(map[string]any{"source": "x"}); err != nil || e != (Entry{Source: "x"}) {
		t.Errorf("source-only entry: e=%+v err=%v", e, err)
	}
	if e, err := parseEntry(nil); err != nil || e != (Entry{}) {
		t.Errorf("nil entry: e=%+v err=%v", e, err)
	}
	if e, err := parseEntry("x"); err != nil || e != (Entry{Source: "x"}) {
		t.Errorf("string entry: e=%+v err=%v", e, err)
	}
	if e, err := parseEntry(map[string]any{"source": "x", "hook": "startup", "root": true}); err != nil ||
		e != (Entry{Source: "x", Hook: "startup", Root: true}) {
		t.Errorf("full entry: e=%+v err=%v", e, err)
	}
}

func TestLoadLayersErrorKeepsHasFalse(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n  source: [\n")
	if _, has, err := LoadLayers([]string{user, proj}); err == nil || has {
		t.Errorf("LoadLayers: has=%v err=%v, want has=false with an error", has, err)
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

func TestLoadLayersPerDirResolution(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n")
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")

	layers, has, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if len(layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(layers))
	}
	if layers[0].Dir != user || layers[1].Dir != proj {
		t.Errorf("layer dirs = %q, %q; want %q, %q", layers[0].Dir, layers[1].Dir, user, proj)
	}
}

func TestLoadLayersNoManifestHasFalse(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	layers, has, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if has {
		t.Error("expected has=false when no manifest exists")
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no pairs, got %d", len(pairs))
	}
}

func TestLoadLayersEmptyManifestHasTrue(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, "")
	layers, has, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if !has {
		t.Error("expected has=true when an (empty) manifest exists")
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no pairs, got %d", len(pairs))
	}
}

func TestResolveSourceEmptyFallsBackToTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveManifestSource(".gitconfig", "", "/tmp/mf")
	if want := filepath.Join(home, ".gitconfig"); got != want {
		t.Errorf("empty source = %q, want %q", got, want)
	}
}

func TestResolveSourceAbsolute(t *testing.T) {
	got := ResolveManifestSource(".x", "/etc/foo", "/tmp/mf")
	if got != "/etc/foo" {
		t.Errorf("expected absolute source, got %q", got)
	}
}

func TestResolveSourceTilde(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveManifestSource(".x", "~/bar", "/tmp/mf")
	if want := filepath.Join(home, "bar"); got != want {
		t.Errorf("~ source = %q, want %q", got, want)
	}
}

func TestResolveSourceRelativeToManifestDir(t *testing.T) {
	got := ResolveManifestSource(".x", "tool/cfg.toml", "/home/u/dotfiles")
	if got != "/home/u/dotfiles/tool/cfg.toml" {
		t.Errorf("expected manifest-dir-relative source, got %q", got)
	}
}

func TestResolveVMTargetExactDotDotReportsEscape(t *testing.T) {
	_, err := ResolveVMTarget(vmHome, "..", nil)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Errorf("ResolveVMTarget(\"..\") error = %v, want an escape error", err)
	}
}

func TestResolveVMTargetDotIsHome(t *testing.T) {
	got, err := ResolveVMTarget(vmHome, ".", nil)
	if err != nil {
		t.Fatalf("ResolveVMTarget(\".\"): %v", err)
	}
	if got != vmHome {
		t.Errorf("ResolveVMTarget(\".\") = %q, want %q", got, vmHome)
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

func TestResolveVMTargetValid(t *testing.T) {
	got, err := ResolveVMTarget(vmHome, ".config/tool/cfg.toml", nil)
	if err != nil {
		t.Fatalf("ResolveVMTarget: %v", err)
	}
	if got != "/home/dev/.config/tool/cfg.toml" {
		t.Errorf("unexpected vm target, got %q", got)
	}
}

func TestResolveVMTargetRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"..", "../evil", "a/../../evil", "/etc/passwd", ""} {
		if _, err := ResolveVMTarget(vmHome, bad, nil); err == nil {
			t.Errorf("expected error for target %q", bad)
		}
	}
}

func TestResolveVMTargetRejectsTilde(t *testing.T) {
	for _, bad := range []string{"~fdsa", "~/fdsa", "~/", "~"} {
		if _, err := ResolveVMTarget(vmHome, bad, nil); err == nil {
			t.Errorf("expected error for target %q", bad)
		}
	}
}

func TestResolveVMTargetReservedPath(t *testing.T) {
	_, err := ResolveVMTarget(vmHome, ".config/opencode/opencode.jsonc", []string{".config/opencode/opencode.jsonc"})
	if err == nil {
		t.Error("expected error for a target listed in reserved")
	}
}

func TestResolveVMTargetEmptyReservedAcceptsAnyPath(t *testing.T) {
	// A previously-reserved opencode path is accepted when reserved is empty,
	// so callers without a reserved merged-config path can provision it.
	got, err := ResolveVMTarget(vmHome, ".config/opencode/opencode.jsonc", nil)
	if err != nil {
		t.Fatalf("ResolveVMTarget with empty reserved: %v", err)
	}
	if got != "/home/dev/.config/opencode/opencode.jsonc" {
		t.Errorf("unexpected vm target, got %q", got)
	}
}

func TestResolveVMTargetRejectsReservedNonCanonicalSpellings(t *testing.T) {
	reserved := []string{".config/opencode/opencode.jsonc"}
	for _, bad := range []string{
		"./.config/opencode/opencode.jsonc",
		".config/opencode//opencode.jsonc",
		".config/opencode/./opencode.jsonc",
	} {
		if _, err := ResolveVMTarget(vmHome, bad, reserved); err == nil {
			t.Errorf("expected error for reserved opencode.json spelled %q", bad)
		}
	}
}

func TestResolveVMTargetAcceptsNonMatchingReservedSpelling(t *testing.T) {
	// The comparison is on the cleaned target, so a different path is accepted.
	got, err := ResolveVMTarget(vmHome, ".config/opencode/opencode.json", []string{".config/opencode/opencode.jsonc"})
	if err != nil {
		t.Fatalf("ResolveVMTarget: %v", err)
	}
	if got != "/home/dev/.config/opencode/opencode.json" {
		t.Errorf("unexpected vm target, got %q", got)
	}
}

func TestBuildHomeFilesEmptyManifestContract(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()

	files, _, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil || has {
		t.Fatalf("no manifest: has=%v err=%v", has, err)
	}
	if files == nil {
		t.Error("expected a non-nil files map when no manifest exists")
	}

	testutil.WriteFile(t, user, "config.yaml", "home: {}\n")
	files, _, has, err = BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil || !has {
		t.Fatalf("empty home key: has=%v err=%v, want has=true", has, err)
	}
	if files == nil {
		t.Error("expected a non-nil files map for an empty manifest")
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %v", files)
	}
}

func TestBuildHomeFilesInvalidTargetKeepsHasFalse(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, "../evil: foo\n")
	if _, _, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil); err == nil || has {
		t.Errorf("invalid target: has=%v err=%v, want has=false with an error", has, err)
	}
}

func TestBuildHomeFilesReportsAllMissingSources(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ""+
		".a: nope-a\n"+
		".b: nope-b\n"+
		".c: nope-c\n"+
		".d: nope-d\n"+
		".e: nope-e\n")
	_, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil || !has {
		t.Fatalf("BuildHomeFilesWithModes: has=%v err=%v", has, err)
	}
	if len(missing) != 5 {
		t.Errorf("missing = %v, want 5 entries", missing)
	}
}

func TestBuildHomeFilesReportsAllUnreadableSources(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	var lines []string
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if err := os.Mkdir(filepath.Join(proj, name), 0o755); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, "."+name+": "+name)
	}
	// The manifest lives next to the directories so each source Stats fine and
	// only ReadFile fails, exercising the read-error continue.
	writeHomeConfig(t, proj, strings.Join(lines, "\n")+"\n")
	_, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil || !has {
		t.Fatalf("BuildHomeFilesWithModes: has=%v err=%v", has, err)
	}
	if len(missing) != 5 {
		t.Errorf("missing = %v, want 5 entries", missing)
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

func TestBuildHomeFilesSkipsMissingSource(t *testing.T) {
	// The mocked home is an empty temp dir, so the empty-source default
	// (host $HOME/.gitconfig) deterministically does not exist.
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n")
	files, missing, _, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if _, ok := files["/home/dev/.gitconfig"]; ok {
		t.Error("expected missing source to be skipped, but entry present")
	}
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing source, got %v", missing)
	}
	want := filepath.Join(home, ".gitconfig")
	if missing[0] != want {
		t.Errorf("missing source = %q, want %q", missing[0], want)
	}
}

func TestBuildHomeFilesRejectsReservedTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, proj, ".config/opencode/opencode.jsonc:\n")
	if _, _, _, err := BuildHomeFilesWithModes(
		user,
		proj,
		vmHome,
		[]string{".config/opencode/opencode.jsonc"},
	); err == nil {
		t.Error("expected an error for a reserved home target")
	}
}

func TestBuildHomeFilesReadsBytesByVMPath(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()

	// project manifest with an absolute source
	src := filepath.Join(proj, "cfg.toml")
	testutil.WritePath(t, src, "k=v\n")
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: "+src+"\n")

	files, _, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := map[string][]byte{
		"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
	}
	if got := homeContents(files); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildHomeFilesWithModesPreservesSourceMode(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	source := filepath.Join(proj, "launcher.sh")
	testutil.WritePath(t, source, "#!/bin/sh\n")
	if err := os.Chmod(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHomeConfig(t, proj, ".config/tool/launcher.sh: ./launcher.sh\n")

	files, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has || len(missing) != 0 {
		t.Fatalf("has = %v, missing = %v; want manifest without missing sources", has, missing)
	}
	file, ok := files["/home/dev/.config/tool/launcher.sh"]
	if !ok {
		t.Fatalf("home file missing: %v", files)
	}
	if string(file.Data) != "#!/bin/sh\n" {
		t.Errorf("file content = %q, want shebang", file.Data)
	}
	if file.Mode.Perm() != 0o755 {
		t.Errorf("file mode = %o, want 755", file.Mode.Perm())
	}
}

// TestBuildHomeFilesWithModesReportsUnreadableSourceAsMissing verifies a source
// that exists but cannot be read as a file (here, a directory) is treated as a
// missing source rather than a fatal error, so a stale home entry never blocks
// VM setup.
func TestBuildHomeFilesWithModesReportsUnreadableSourceAsMissing(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	sourceDir := filepath.Join(proj, "not-a-file")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHomeConfig(t, proj, ".config/tool/x: "+sourceDir+"\n")

	files, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if len(files) != 0 {
		t.Errorf("unreadable source must yield no home file, got %v", files)
	}
	if len(missing) != 1 || missing[0] != sourceDir {
		t.Errorf("missing = %v, want [%q]", missing, sourceDir)
	}
}

func TestBuildHomeFilesReadsProjectRelativeSourceFromProjectDir(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()

	// project manifest with a source relative to the project manifest dir
	if err := os.MkdirAll(filepath.Join(proj, "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, proj, "tool/cfg.toml", "k=v\n")
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")

	files, _, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := map[string][]byte{
		"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
	}
	if got := homeContents(files); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDescribeLayersKeepsUserOnlyKeys covers the half of the retired
// MergeManifests wrapper's behaviour that TestDescribeLayersProjectWinsOnOverlap
// does not: a key only the user layer declares survives the merge.
func TestDescribeLayersKeepsUserOnlyKeys(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".ssh/config:\n")
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./tool.toml\n")

	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	for _, p := range pairs {
		if p[0] == "/home/dev/.ssh/config" {
			return
		}
	}
	t.Errorf("a key only the user layer declares should remain, got %v", pairs)
}

func TestDescribeLayersRejectsReservedTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, proj, ".pi/agent/settings.json:\n")
	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if _, err := DescribeLayers(layers, vmHome, []string{".pi/agent/settings.json"}); err == nil {
		t.Error("expected an error for a reserved home target")
	}
}

func TestDescribeLayersResolvesProjectRelativeAgainstProjectDir(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")

	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	want := filepath.Join(proj, "tool/cfg.toml")
	var src string
	for _, p := range pairs {
		if p[0] == "/home/dev/.config/tool/cfg.toml" {
			src = p[1]
		}
	}
	if src != want {
		t.Errorf("got source %q, want %q", src, want)
	}
}

func TestDescribeLayersSortsPairsByVMPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, proj, ""+
		".zshrc:\n"+
		".a:\n"+
		".M:\n"+
		".config/tool/cfg.toml: ./cfg.toml\n"+
		".b:\n")

	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	var paths []string
	for _, p := range pairs {
		paths = append(paths, p[0])
	}
	want := []string{
		"/home/dev/.M",
		"/home/dev/.a",
		"/home/dev/.b",
		"/home/dev/.config/tool/cfg.toml",
		"/home/dev/.zshrc",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("DescribeLayers pairs not sorted by VM path:\ngot  %v\nwant %v", paths, want)
	}
}

func TestDescribeLayersProjectWinsOnOverlap(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".config/tool/cfg.toml: ./user.toml\n")
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./proj.toml\n")

	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected exactly 1 pair (project wins), got %d: %v", len(pairs), pairs)
	}
	want := filepath.Join(proj, "proj.toml")
	if pairs[0][1] != want {
		t.Errorf("expected project source %q, got %q", want, pairs[0][1])
	}
}

func TestDescribePairLess(t *testing.T) {
	a := [2]string{"/a", "src-a"}
	b := [2]string{"/b", "src-b"}
	if !describePairLess(a, b) {
		t.Error("/a must sort before /b")
	}
	if describePairLess(b, a) {
		t.Error("/b must not sort before /a")
	}
	if describePairLess(a, a) {
		t.Error("equal targets must not sort before each other")
	}
}

func TestHasShebangMissingFile(t *testing.T) {
	if got := hasShebang(filepath.Join(t.TempDir(), "nope.sh")); got {
		t.Errorf("expected false for missing file, got true")
	}
}

func TestHasShebang(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"plain sh", "#!/bin/sh\n", true},
		{"bash", "#!/bin/bash\necho hi\n", true},
		{"env bash", "#!/usr/bin/env bash\n", true},
		{"with spaces", "#!/usr/bin/env python3\n", true},
		{"with arg", "#!/bin/bash -e\n", true},
		{"no shebang", "echo hi\n", false},
		{"empty file", "", false},
		{"bare marker", "#! \n", true},
		{"crlf shebang", "#!/bin/bash\r\n", true},
		{"no trailing newline", "#!/bin/bash", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, "s.sh")
			testutil.WritePath(t, p, tc.body)
			if got := hasShebang(p); got != tc.want {
				t.Errorf("hasShebang(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestBuildHooksSkipsNonStartupEntries pins that a non-hook entry is skipped,
// not promoted into a hook, even when its source exists.
func TestBuildHooksSkipsNonStartupEntries(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "a.sh"), "#!/bin/sh\n")
	testutil.WritePath(t, filepath.Join(proj, "b.sh"), "#!/bin/sh\n")
	writeHomeConfig(t, proj, ""+
		".a:\n  source: a.sh\n"+
		".b:\n  source: b.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].Target != "/home/dev/.b" {
		t.Errorf("hooks = %v, want only /home/dev/.b", hooks)
	}
}

// TestBuildHooksContinuesPastMissingSource pins that a hook whose source is
// missing is skipped and later hooks are still processed.
func TestBuildHooksContinuesPastMissingSource(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "b.sh"), "#!/bin/sh\n")
	writeHomeConfig(t, proj, ""+
		".a:\n  source: missing-a.sh\n  hook: startup\n"+
		".b:\n  source: b.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].Target != "/home/dev/.b" {
		t.Errorf("hooks = %v, want only /home/dev/.b", hooks)
	}
}

// TestBuildHooksSortsByVMTargetOrder pins that BuildHooks re-orders by the
// resolved VM target, not by the manifest key: the keys "./b" and "a" iterate
// in the opposite order to their VM targets, so the final sort must reorder
// them.
func TestBuildHooksSortsByVMTargetOrder(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "a.sh"), "#!/bin/sh\n")
	testutil.WritePath(t, filepath.Join(proj, "b.sh"), "#!/bin/sh\n")
	writeHomeConfig(t, proj, ""+
		"./b:\n  source: b.sh\n  hook: startup\n"+
		"a:\n  source: a.sh\n  hook: startup\n")

	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	want := []string{"/home/dev/a", "/home/dev/b"}
	if len(hooks) != 2 || hooks[0].Target != want[0] || hooks[1].Target != want[1] {
		t.Errorf("hooks = %v, want targets %v", hooks, want)
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

func TestBuildHooksFiltersAndSorts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()

	// Only .vpn/connect.sh is marked hook: startup and its source exists.
	if err := os.MkdirAll(filepath.Join(proj, "vpn"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(proj, "vpn/connect.sh"), "#!/bin/sh\necho hi\n")
	writeHomeConfig(t, proj, ""+
		".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n  root: true\n"+
		".zshrc:\n")

	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	want := []HookSpec{
		{
			Target:     "/home/dev/.vpn/connect.sh",
			Source:     filepath.Join(proj, "vpn/connect.sh"),
			HasShebang: true,
			Root:       true,
		},
	}
	if !reflect.DeepEqual(hooks, want) {
		t.Errorf("got %v, want %v", hooks, want)
	}
}

func TestBuildHooksSkipsMissingSource(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	// hook entry whose source file does not exist on the host
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 0 {
		t.Errorf("expected no hooks for missing source, got %v", hooks)
	}
}

func TestBuildHooksProjectWinsOnOverlap(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "vpn"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(user, "vpn"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(proj, "vpn/connect.sh"), "#!/bin/sh\necho proj\n")
	testutil.WritePath(t, filepath.Join(user, "vpn/connect.sh"), "#!/bin/sh\necho user\n")
	writeHomeConfig(t, user, ".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n  root: true\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n  root: true\n")

	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("expected exactly 1 hook (project wins), got %d: %v", len(hooks), hooks)
	}
	want := filepath.Join(proj, "vpn/connect.sh")
	if hooks[0].Source != want {
		t.Errorf("expected project source %q, got %q", want, hooks[0].Source)
	}
}

func TestBuildHooksCapturesShebang(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "connect.sh"), "#!/bin/bash\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 || !hooks[0].HasShebang {
		t.Errorf("hooks = %v, want a hook with a shebang", hooks)
	}
}

func TestBuildHooksCapturesEnvShebang(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "connect.sh"), "#!/usr/bin/env bash\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks = %v, want 1 hook", hooks)
	}
	if !hooks[0].HasShebang {
		t.Errorf("hooks[0].HasShebang = %v, want true", hooks[0].HasShebang)
	}
}

func TestBuildHooksNoShebangFallsBack(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "connect.sh"), "echo hi\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks = %v, want 1 hook", hooks)
	}
	if hooks[0].HasShebang {
		t.Errorf("hooks[0].HasShebang = %v, want false", hooks[0].HasShebang)
	}
}

func TestBuildHooksSortsByTarget(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "a.sh"), "#!/bin/sh\n")
	testutil.WritePath(t, filepath.Join(proj, "b.sh"), "#!/bin/sh\n")
	writeHomeConfig(t, proj, ""+
		".b:\n  source: b.sh\n  hook: startup\n"+
		".a:\n  source: a.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 2 || hooks[0].Target != "/home/dev/.a" || hooks[1].Target != "/home/dev/.b" {
		t.Errorf("hooks not sorted by target: %v", hooks)
	}
}

func TestHookSpecLess(t *testing.T) {
	a := HookSpec{Target: "/a"}
	b := HookSpec{Target: "/b"}
	if !hookSpecLess(a, b) {
		t.Error("/a must sort before /b")
	}
	if hookSpecLess(b, a) {
		t.Error("/b must not sort before /a")
	}
	if hookSpecLess(a, a) {
		t.Error("equal targets must not sort before each other")
	}
}

const vmHome = "/home/dev"

// testData wraps an inner home body under a top-level home: key and writes it
// as the user config file.
func writeHomeConfig(t *testing.T, dir, body string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("home:\n")
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sb.WriteString("  " + line + "\n")
	}
	testutil.WriteFile(t, dir, "config.yaml", sb.String())
}

// homeContents reduces the with-modes result to the byte map the retired
// BuildHomeFiles wrapper returned, so these tests keep asserting exactly the
// content they asserted before the wrapper was deleted.
func homeContents(files map[string]HomeFile) map[string][]byte {
	contents := make(map[string][]byte, len(files))
	for path, file := range files {
		contents[path] = file.Data
	}
	return contents
}

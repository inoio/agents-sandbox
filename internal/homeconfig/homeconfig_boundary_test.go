package homeconfig

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/titanous/json5"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

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

func TestReadHomeFromConfigDirJSONCIsNormalized(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.jsonc", `{
		// a json5-only comment line
		"home": {".gitconfig": ""},
	}`)
	m, has, err := ReadHomeFromConfigDir(dir)
	if err != nil || !has {
		t.Fatalf("ReadHomeFromConfigDir: has=%v err=%v", has, err)
	}
	if _, ok := m[".gitconfig"]; !ok {
		t.Errorf("expected .gitconfig entry, got %v", m)
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

func TestLoadLayersErrorKeepsHasFalse(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n  source: [\n")
	if _, has, err := LoadLayers([]string{user, proj}); err == nil || has {
		t.Errorf("LoadLayers: has=%v err=%v, want has=false with an error", has, err)
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

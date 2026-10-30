package configmerge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestBuildMergedRecursivelyMergesNestedMaps(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-a.json", `{"models": {"a": 1}}`)
	writeSnippet(t, dir, "opencode-b.json", `{"models": {"b": 2}}`)

	built, err := BuildMerged("", "opencode*.json*", dir, "")
	if err != nil || !built.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", built.HasSnippets, err)
	}
	var m map[string]map[string]int
	if err := json.Unmarshal(built.Content, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["models"]["a"] != 1 || m["models"]["b"] != 2 {
		t.Errorf("nested merge = %v, want models.a=1 models.b=2", m)
	}
}

func TestBuildMergedNonMapOverrideReplacesNestedMap(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-a.json", `{"models": {"a": 1}}`)
	writeSnippet(t, dir, "opencode-b.json", `{"models": 5}`)

	built, err := BuildMerged("", "opencode*.json*", dir, "")
	if err != nil || !built.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", built.HasSnippets, err)
	}
	var m map[string]any
	if err := json.Unmarshal(built.Content, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["models"] != float64(5) {
		t.Errorf("override = %v, want 5", m["models"])
	}
}

func TestBuildMergedSkipsMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-a.json", `{"a": 1}`)

	built, err := BuildMerged("", "opencode*.json*", dir, filepath.Join(t.TempDir(), "missing"))
	if err != nil || !built.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", built.HasSnippets, err)
	}
	if len(built.Sources) != 1 {
		t.Fatalf("sources = %v, want only the file from the existing dir", built.Sources)
	}
	var m map[string]int
	if err := json.Unmarshal(built.Content, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["a"] != 1 {
		t.Errorf("merged = %v, want a=1", m)
	}
}

func TestBuildMergedScansDirectoryAfterMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-a.json", `{"a": 1}`)
	missing := filepath.Join(t.TempDir(), "missing")

	// The missing dir is first: a broken skip must not abandon the dirs loop.
	built, err := BuildMerged("", "opencode*.json*", missing, dir)
	if err != nil || !built.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", built.HasSnippets, err)
	}
	if len(built.Sources) != 1 || built.Sources[0] != filepath.Join(dir, "opencode-a.json") {
		t.Fatalf("sources = %v, want only the file from the second dir", built.Sources)
	}
	var m map[string]int
	if err := json.Unmarshal(built.Content, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["a"] != 1 {
		t.Errorf("merged = %v, want a=1", m)
	}
}

func TestScanSnippetsReturnsNonNilEmptyMap(t *testing.T) {
	merged, sources := scanSnippets([]string{t.TempDir()}, "opencode*.json*")
	if len(sources) != 0 {
		t.Errorf("sources = %v, want none", sources)
	}
	if merged == nil {
		t.Error("merged = nil, want a non-nil empty map")
	}
	if len(merged) != 0 {
		t.Errorf("merged = %v, want empty", merged)
	}
}

func TestScanSnippetsSkipsDirectoryOnPartialReadError(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-a.json", `{"a": 1}`)

	// os.ReadDir can return the entries it read before an error. Removing the
	// guard would merge that partial entry instead of skipping the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	restore := readDir
	readDir = func(string) ([]os.DirEntry, error) {
		return entries, errors.New("partial read")
	}
	t.Cleanup(func() { readDir = restore })

	merged, sources := scanSnippets([]string{dir}, "opencode*.json*")
	if len(sources) != 0 {
		t.Errorf("sources = %v, want none", sources)
	}
	if len(merged) != 0 {
		t.Errorf("merged = %v, want empty", merged)
	}
}

func TestBuildMergedSkipsUnreadableSnippet(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-good.json", `{"ok": true}`)
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "opencode-broken.json")); err != nil {
		t.Fatal(err)
	}

	built, err := BuildMerged("", "opencode*.json*", dir, "")
	if err != nil || !built.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", built.HasSnippets, err)
	}
	if len(built.Sources) != 1 {
		t.Errorf("sources = %v, want only the readable snippet", built.Sources)
	}
	var m map[string]bool
	if err := json.Unmarshal(built.Content, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !m["ok"] {
		t.Errorf("merged = %v, want ok=true", m)
	}
}

func TestBuildMergedSkipsUnreadableYAMLSnippet(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "pi-broken.yaml")); err != nil {
		t.Fatal(err)
	}

	// The only match is unreadable; an empty YAML document would otherwise
	// parse to a nil config and falsely report a merge.
	built, err := BuildMerged("", "pi-*.{json,yaml}", dir, "")
	if err != nil {
		t.Fatalf("BuildMerged: %v", err)
	}
	if built.HasSnippets {
		t.Errorf("expected has=false for an unreadable YAML snippet, got data=%q", built.Content)
	}
	if len(built.Sources) != 0 {
		t.Errorf("sources = %v, want none", built.Sources)
	}
}

func TestBuildMergedMarshalError(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-inf.yaml", "v: !!float .inf\n")

	built, err := BuildMerged("", "opencode-*", dir, "")
	if err == nil {
		t.Fatal("expected marshal error for a non-finite float, got nil")
	}
	if built.HasSnippets {
		t.Error("expected has=false when marshaling fails")
	}
	if built.Content != nil || built.Sources != nil {
		t.Errorf("expected nil data and sources on error, got data=%v sources=%v", built.Content, built.Sources)
	}
}

func TestScanMirrorSkipsUnreadableEntries(t *testing.T) {
	user := t.TempDir()
	dest := t.TempDir()
	testutil.WriteFile(t, user, "ok.json", `{"ok": 1}`)

	locked := filepath.Join(user, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, locked, "secret.json", `{"s": 1}`)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	noread := filepath.Join(user, "noread.json")
	if err := os.WriteFile(noread, []byte(`{}`), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(noread, 0o644) })

	entries, err := ScanMirror("opencode*.json*", nil, user, "", dest)
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	if len(entries) != 1 || entries[0].HostPath != filepath.Join(user, "ok.json") {
		t.Errorf("entries = %+v, want only ok.json", entries)
	}
}

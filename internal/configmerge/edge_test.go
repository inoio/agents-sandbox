package configmerge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestBuildMergedRecursivelyMergesNestedMaps(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-a.json", `{"models": {"a": 1}}`)
	writeSnippet(t, dir, "opencode-b.json", `{"models": {"b": 2}}`)

	data, _, has, err := BuildMerged("opencode*.json*", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMerged: has=%v err=%v", has, err)
	}
	var m map[string]map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
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

	data, _, has, err := BuildMerged("opencode*.json*", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMerged: has=%v err=%v", has, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["models"] != float64(5) {
		t.Errorf("override = %v, want 5", m["models"])
	}
}

func TestBuildMergedSkipsMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-a.json", `{"a": 1}`)

	data, sources, has, err := BuildMerged("opencode*.json*", dir, filepath.Join(t.TempDir(), "missing"))
	if err != nil || !has {
		t.Fatalf("BuildMerged: has=%v err=%v", has, err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %v, want only the file from the existing dir", sources)
	}
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["a"] != 1 {
		t.Errorf("merged = %v, want a=1", m)
	}
}

func TestBuildMergedSkipsUnreadableSnippet(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-good.json", `{"ok": true}`)
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "opencode-broken.json")); err != nil {
		t.Fatal(err)
	}

	data, sources, has, err := BuildMerged("opencode*.json*", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMerged: has=%v err=%v", has, err)
	}
	if len(sources) != 1 {
		t.Errorf("sources = %v, want only the readable snippet", sources)
	}
	var m map[string]bool
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !m["ok"] {
		t.Errorf("merged = %v, want ok=true", m)
	}
}

func TestBuildMergedMarshalError(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "opencode-inf.yaml", "v: !!float .inf\n")

	data, sources, has, err := BuildMerged("opencode-*", dir, "")
	if err == nil {
		t.Fatal("expected marshal error for a non-finite float, got nil")
	}
	if has {
		t.Error("expected has=false when marshaling fails")
	}
	if data != nil || sources != nil {
		t.Errorf("expected nil data and sources on error, got data=%v sources=%v", data, sources)
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

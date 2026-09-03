package configmerge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func writeSnippet(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, name, content)
}

func TestSnippetFileMatches(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"opencode-*.json*", "opencode-x.json", true},
		{"opencode-*.json*", "opencode-x.jsonc", true},
		{"opencode-*.json*", "opencode-x.json5", true},
		{"opencode-*.json*", "opencode-a.json", true},
		{"opencode-*.json*", "opencode.json", false},
		{"opencode-*.json*", "opencode.jsonc", false},
		{"opencode-*.json*", "auth.json", false},
		{"opencode-*.json*", "tui.json5", false},
		{"opencode-*.json*", "README.md", false},
		{"opencode-*.json*", "opencode.txt", false},
		{"pi-*.{json,yaml}", "pi-settings.yaml", true},
		{"pi-*.{json,yaml}", "pi-settings.json", true},
		{"pi-*.{json,yaml}", "pi-settings.yml", false},
		{"pi-*.{json,yaml}", "pi-settings.json5", false},
		{"pi-*.{json,yaml}", "other.yaml", false},
	}
	for _, c := range cases {
		if got := snippetFileMatches(c.pattern, c.name); got != c.want {
			t.Errorf("snippetFileMatches(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestBuildMergedPatternFilter(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "opencode-a.json", `{"a":1}`)
	testutil.WriteFile(t, dir, "opencode-b.json", `{"b":2}`)
	testutil.WriteFile(t, dir, "ignored.json", `{"x":9}`) // must not merge (name doesn't match)
	data, sources, has, err := BuildMerged("opencode-*.json*", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMerged: has=%v err=%v", has, err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %d, want 2 (ignored.json excluded)", len(sources))
	}
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["a"] != 1 || m["b"] != 2 {
		t.Errorf("merged = %v, want a=1 b=2", m)
	}
	if _, hasX := m["x"]; hasX {
		t.Error("ignored.json content should not be merged")
	}
}

func TestBuildMergedYAML(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "pi-settings.yaml", "theme: dark\nprovider: anthropic\n")
	data, _, has, err := BuildMerged("pi-*.{json,yaml}", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMerged yaml: has=%v err=%v", has, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if m["theme"] != "dark" || m["provider"] != "anthropic" {
		t.Errorf("yaml merged = %v", m)
	}
}

func TestBuildMergedSourcesInMergeOrder(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeSnippet(t, user, "opencode-02-user.json", `{"a": 1}`)
	writeSnippet(t, user, "opencode-01-user.json", `{"a": 2}`)
	writeSnippet(t, proj, "opencode-proj.json", `{"b": 3}`)

	_, sources, has, err := BuildMerged("opencode-*.json*", user, proj)
	if err != nil || !has {
		t.Fatalf("BuildMerged: has=%v err=%v", has, err)
	}
	want := []string{
		filepath.Join(user, "opencode-01-user.json"),
		filepath.Join(user, "opencode-02-user.json"),
		filepath.Join(proj, "opencode-proj.json"),
	}
	if len(sources) != len(want) {
		t.Fatalf("sources = %v, want %v", sources, want)
	}
	for i := range want {
		if sources[i] != want[i] {
			t.Fatalf("sources = %v, want %v", sources, want)
		}
	}
}

func TestBuildMergedNoSnippets(t *testing.T) {
	data, sources, has, err := BuildMerged("opencode-*.json*", t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("BuildMerged: %v", err)
	}
	if has {
		t.Error("expected has=false when no snippet files exist")
	}
	if data != nil {
		t.Errorf("expected nil bytes when no snippets, got %q", data)
	}
	if sources != nil {
		t.Errorf("expected nil sources when no snippets, got %v", sources)
	}
}

func TestBuildMergedEmitsMergedJSON(t *testing.T) {
	user := t.TempDir()
	writeSnippet(t, user, "opencode-x.json5", `{"model": "x", "instructions": "be brief"}`)

	data, sources, has, err := BuildMerged("opencode-*.json*", user, "")
	if err != nil {
		t.Fatalf("BuildMerged: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if len(sources) != 1 || sources[0] != filepath.Join(user, "opencode-x.json5") {
		t.Errorf("unexpected sources: %v", sources)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if parsed["model"] != "x" || parsed["instructions"] != "be brief" {
		t.Errorf("unexpected merged content: %v", parsed)
	}
}

func TestBuildMergedDeterministic(t *testing.T) {
	user := t.TempDir()
	writeSnippet(t, user, "opencode-a.json", `{"b": 1, "a": 2}`)
	writeSnippet(t, user, "opencode-c.json5", `{"c": {"z": 1, "y": 2}}`)

	var first []byte
	for range 5 {
		data, _, has, err := BuildMerged("opencode-*.json*", user, "")
		if err != nil || !has {
			t.Fatalf("BuildMerged: has=%v err=%v", has, err)
		}
		if first == nil {
			first = data
			continue
		}
		if !bytes.Equal(first, data) {
			t.Fatal("output not deterministic across runs")
		}
	}
	// sanity: no trailing-extra blank lines weirdness
	if !strings.HasSuffix(string(first), "\n") {
		t.Error("expected a single trailing newline")
	}
}

// BuildMerged is the generic entry point: pattern comes from the caller, so the
// package no longer hardcodes the opencode snippet pattern.
func TestBuildMergedUsesCallerPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pi-model.yaml"), []byte("model: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, sources, has, err := BuildMerged("pi-*.{json,yaml}", "", dir)
	if err != nil {
		t.Fatalf("BuildMerged: %v", err)
	}
	if !has || len(sources) != 1 {
		t.Fatalf("has=%v sources=%v, want one matched file", has, sources)
	}
	if !strings.Contains(string(data), `"model": "x"`) {
		t.Errorf("merged data = %s, want model:x", data)
	}
}

func TestExpandBracesUnbalanced(t *testing.T) {
	// Unmatched open or close brace: returned unchanged.
	if got := expandBraces("a-{b,c"); len(got) != 1 || got[0] != "a-{b,c" {
		t.Errorf("unmatched open brace = %v, want [a-{b,c]", got)
	}
	if got := expandBraces("a-b}"); len(got) != 1 || got[0] != "a-b}" {
		t.Errorf("unmatched close brace = %v, want [a-b}]", got)
	}
}

func TestBuildMergedSkipsUnparsableSnippet(t *testing.T) {
	dir := t.TempDir()
	// A matching-but-invalid snippet must be skipped, not fail the build.
	writeSnippet(t, dir, "opencode-bad.json5", "{ this is not valid json5")
	writeSnippet(t, dir, "opencode-good.json", `{"ok": true}`)

	data, sources, has, err := BuildMerged("opencode-*.json*", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMerged: has=%v err=%v", has, err)
	}
	if len(sources) != 1 {
		t.Errorf("sources = %v, want only the valid snippet", sources)
	}
	var m map[string]bool
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !m["ok"] {
		t.Errorf("merged = %v, want ok=true", m)
	}
}

func TestScanMirrorBasic(t *testing.T) {
	user := t.TempDir()
	dest := t.TempDir()
	testutil.WriteFile(t, user, "tui.json", `{"theme":"dark"}`)
	os.MkdirAll(filepath.Join(user, "agents"), 0o755)
	os.MkdirAll(filepath.Join(user, "themes"), 0o755)
	testutil.WriteFile(t, filepath.Join(user, "agents"), "coder.md", "# coder\n")
	testutil.WriteFile(t, filepath.Join(user, "themes"), "dark.json", `{"bg":"#000"}`)

	entries, err := ScanMirror("opencode-*.json*", nil, user, "", dest)
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3: %+v", len(entries), entries)
	}
	want := map[string]string{
		filepath.Join(dest, "tui.json"):            filepath.Join(user, "tui.json"),
		filepath.Join(dest, "agents", "coder.md"):  filepath.Join(user, "agents", "coder.md"),
		filepath.Join(dest, "themes", "dark.json"): filepath.Join(user, "themes", "dark.json"),
	}
	for _, e := range entries {
		wantHost, ok := want[e.VMPath]
		if !ok {
			t.Errorf("unexpected VMPath %q", e.VMPath)
			continue
		}
		if e.HostPath != wantHost {
			t.Errorf("VMPath %q HostPath = %q, want %q", e.VMPath, e.HostPath, wantHost)
		}
		if len(e.Data) == 0 {
			t.Errorf("VMPath %q has empty Data", e.VMPath)
		}
	}
}

func TestScanMirrorSkipsSnippetPatternAndFamily(t *testing.T) {
	user := t.TempDir()
	dest := t.TempDir()
	testutil.WriteFile(t, user, "opencode-model.json", `{"model":"x"}`) // snippet pattern
	testutil.WriteFile(t, user, "opencode.jsonc", `{"a":1}`)            // config-family name
	testutil.WriteFile(t, user, "tui.json", `{"theme":"dark"}`)

	family := []string{"config.json", "opencode.json", "opencode.jsonc", "opencode.json5"}
	entries, err := ScanMirror("opencode-*.json*", family, user, "", dest)
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (only tui.json): %+v", len(entries), entries)
	}
	if entries[0].VMPath != filepath.Join(dest, "tui.json") {
		t.Errorf("VMPath = %q, want %q", entries[0].VMPath, filepath.Join(dest, "tui.json"))
	}
}

func TestScanMirrorProjectOverridesUser(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	dest := t.TempDir()
	testutil.WriteFile(t, user, "tui.json", `{"theme":"user"}`)
	testutil.WriteFile(t, proj, "tui.json", `{"theme":"project"}`)

	entries, err := ScanMirror("opencode-*.json*", nil, user, proj, dest)
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].HostPath != filepath.Join(proj, "tui.json") {
		t.Errorf("HostPath = %q, want project path", entries[0].HostPath)
	}
	if !bytes.Contains(entries[0].Data, []byte("project")) {
		t.Errorf("Data = %q, want project content", entries[0].Data)
	}
}

func TestScanMirrorEmptyDirs(t *testing.T) {
	entries, err := ScanMirror("opencode-*.json*", nil, t.TempDir(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want none", entries)
	}
}

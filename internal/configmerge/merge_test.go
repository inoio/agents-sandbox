package configmerge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/testutil"
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
		{"opencode*.json*", "opencode-x.json", true},
		{"opencode*.json*", "opencode-x.jsonc", true},
		{"opencode*.json*", "opencode-x.json5", true},
		{"opencode*.json*", "opencode-a.json", true},
		{"opencode*.json*", "opencode.json", true},
		{"opencode*.json*", "opencode.jsonc", true},
		{"opencode*.json*", "auth.json", false},
		{"opencode*.json*", "tui.json5", false},
		{"opencode*.json*", "README.md", false},
		{"opencode*.json*", "opencode.txt", false},
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
	merged, err := BuildMerged("", "opencode*.json*", dir, "")
	if err != nil || !merged.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", merged.HasSnippets, err)
	}
	if len(merged.Sources) != 2 {
		t.Fatalf("sources = %d, want 2 (ignored.json excluded)", len(merged.Sources))
	}
	var m map[string]int
	if err := json.Unmarshal(merged.Content, &m); err != nil {
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
	merged, err := BuildMerged("", "pi-*.{json,yaml}", dir, "")
	if err != nil || !merged.HasSnippets {
		t.Fatalf("BuildMerged yaml: has=%v err=%v", merged.HasSnippets, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(merged.Content, &m); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if m["theme"] != "dark" || m["provider"] != "anthropic" {
		t.Errorf("yaml merged = %v", m)
	}
}

func TestBuildMergedYAMLTargetEmitsYAML(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config-model.yaml", "model: opus\n")
	testutil.WriteFile(t, dir, "config-tools.yaml", "tools:\n  web: true\n")
	merged, err := BuildMerged("/home/dev/.hermes/config.yaml", "config*.yaml", dir, "")
	if err != nil || !merged.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", merged.HasSnippets, err)
	}
	// A YAML config path must yield YAML, not a JSON document wearing a .yaml
	// name: the merged file is both the agent's input and user-editable.
	if strings.HasPrefix(strings.TrimSpace(string(merged.Content)), "{") {
		t.Fatalf("merged config for a .yaml target is JSON:\n%s", merged.Content)
	}
	want := "model: opus\ntools:\n    web: true\n"
	if string(merged.Content) != want {
		t.Errorf("merged YAML = %q, want %q", merged.Content, want)
	}
}

func TestBuildMergedYMLTargetEmitsYAML(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config-settings.yml", "model: opus\n")
	merged, err := BuildMerged("/home/dev/.hermes/config.yml", "config*.yml", dir, "")
	if err != nil || !merged.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", merged.HasSnippets, err)
	}
	if string(merged.Content) != "model: opus\n" {
		t.Errorf("merged YAML = %q, want %q", merged.Content, "model: opus\n")
	}
}

func TestBuildMergedJSONTargetStaysJSON(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "opencode-a.json", `{"a":1}`)
	merged, err := BuildMerged("/home/dev/.config/opencode/opencode.jsonc", "opencode*.json*", dir, "")
	if err != nil || !merged.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", merged.HasSnippets, err)
	}
	want := "{\n  \"a\": 1\n}\n"
	if string(merged.Content) != want {
		t.Errorf("merged JSON = %q, want %q", merged.Content, want)
	}
}

func TestBuildMergedSourcesInMergeOrder(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeSnippet(t, user, "opencode-02-user.json", `{"a": 1}`)
	writeSnippet(t, user, "opencode-01-user.json", `{"a": 2}`)
	writeSnippet(t, proj, "opencode-proj.json", `{"b": 3}`)

	merged, err := BuildMerged("", "opencode*.json*", user, proj)
	if err != nil || !merged.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", merged.HasSnippets, err)
	}
	want := []string{
		filepath.Join(user, "opencode-01-user.json"),
		filepath.Join(user, "opencode-02-user.json"),
		filepath.Join(proj, "opencode-proj.json"),
	}
	if len(merged.Sources) != len(want) {
		t.Fatalf("sources = %v, want %v", merged.Sources, want)
	}
	for i := range want {
		if merged.Sources[i] != want[i] {
			t.Fatalf("sources = %v, want %v", merged.Sources, want)
		}
	}
}

func TestBuildMergedNoSnippets(t *testing.T) {
	merged, err := BuildMerged("", "opencode*.json*", t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("BuildMerged: %v", err)
	}
	if merged.HasSnippets {
		t.Error("expected has=false when no snippet files exist")
	}
	if merged.Content != nil {
		t.Errorf("expected nil bytes when no snippets, got %q", merged.Content)
	}
	if merged.Sources != nil {
		t.Errorf("expected nil sources when no snippets, got %v", merged.Sources)
	}
}

func TestBuildMergedEmitsMergedJSON(t *testing.T) {
	user := t.TempDir()
	writeSnippet(t, user, "opencode-x.json5", `{"model": "x", "instructions": "be brief"}`)

	merged, err := BuildMerged("", "opencode*.json*", user, "")
	if err != nil {
		t.Fatalf("BuildMerged: %v", err)
	}
	if !merged.HasSnippets {
		t.Fatal("expected has=true")
	}
	if len(merged.Sources) != 1 || merged.Sources[0] != filepath.Join(user, "opencode-x.json5") {
		t.Errorf("unexpected sources: %v", merged.Sources)
	}
	var parsed map[string]any
	if err := json.Unmarshal(merged.Content, &parsed); err != nil {
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
		merged, err := BuildMerged("", "opencode*.json*", user, "")
		if err != nil || !merged.HasSnippets {
			t.Fatalf("BuildMerged: has=%v err=%v", merged.HasSnippets, err)
		}
		if first == nil {
			first = merged.Content
			continue
		}
		if !bytes.Equal(first, merged.Content) {
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
	merged, err := BuildMerged("", "pi-*.{json,yaml}", "", dir)
	if err != nil {
		t.Fatalf("BuildMerged: %v", err)
	}
	if !merged.HasSnippets || len(merged.Sources) != 1 {
		t.Fatalf("has=%v sources=%v, want one matched file", merged.HasSnippets, merged.Sources)
	}
	if !strings.Contains(string(merged.Content), `"model": "x"`) {
		t.Errorf("merged data = %s, want model:x", merged.Content)
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

func TestExpandBracesStartingWithBrace(t *testing.T) {
	// open == 0 is a real brace group, not "no brace".
	cases := []struct {
		pattern string
		want    []string
	}{
		{"{a,b}", []string{"a", "b"}},
		{"{a,b}x", []string{"ax", "bx"}},
	}
	for _, c := range cases {
		if got := expandBraces(c.pattern); !slices.Equal(got, c.want) {
			t.Errorf("expandBraces(%q) = %v, want %v", c.pattern, got, c.want)
		}
	}
}

func TestDeepMergeMergesEverySharedKey(t *testing.T) {
	base := map[string]any{
		"m": map[string]any{"a": 1},
		"n": map[string]any{"c": 3},
	}
	override := map[string]any{
		"m": map[string]any{"b": 2},
		"n": map[string]any{"d": 4},
	}
	got := deepMerge(base, override)
	m, _ := got["m"].(map[string]any)
	n, _ := got["n"].(map[string]any)
	if m["a"] != 1 || m["b"] != 2 {
		t.Errorf("m = %v, want a=1 b=2", m)
	}
	if n["c"] != 3 || n["d"] != 4 {
		t.Errorf("n = %v, want c=3 d=4", n)
	}
}

func TestBuildMergedSkipsUnparsableSnippet(t *testing.T) {
	dir := t.TempDir()
	// A matching-but-invalid snippet must be skipped, not fail the build.
	writeSnippet(t, dir, "opencode-bad.json5", "{ this is not valid json5")
	writeSnippet(t, dir, "opencode-good.json", `{"ok": true}`)

	merged, err := BuildMerged("", "opencode*.json*", dir, "")
	if err != nil || !merged.HasSnippets {
		t.Fatalf("BuildMerged: has=%v err=%v", merged.HasSnippets, err)
	}
	if len(merged.Sources) != 1 {
		t.Errorf("sources = %v, want only the valid snippet", merged.Sources)
	}
	var m map[string]bool
	if err := json.Unmarshal(merged.Content, &m); err != nil {
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
	if err := os.Chmod(filepath.Join(user, "tui.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(user, "agents"), 0o755)
	os.MkdirAll(filepath.Join(user, "themes"), 0o755)
	testutil.WriteFile(t, filepath.Join(user, "agents"), "coder.md", "# coder\n")
	testutil.WriteFile(t, filepath.Join(user, "themes"), "dark.json", `{"bg":"#000"}`)
	if err := os.Symlink("missing-target", filepath.Join(user, "broken-link")); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanMirror("opencode*.json*", nil, user, "", dest)
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
		if e.VMPath == filepath.Join(dest, "tui.json") && e.Mode.Perm() != 0o755 {
			t.Errorf("VMPath %q mode = %o, want 755", e.VMPath, e.Mode.Perm())
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
	entries, err := ScanMirror("opencode*.json*", family, user, "", dest)
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
	if err := os.Chmod(filepath.Join(user, "tui.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(proj, "tui.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanMirror("opencode*.json*", nil, user, proj, dest)
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
	if entries[0].Mode.Perm() != 0o700 {
		t.Errorf("Mode = %o, want 700", entries[0].Mode.Perm())
	}
}

func TestScanMirrorEmptyDirs(t *testing.T) {
	entries, err := ScanMirror("opencode*.json*", nil, t.TempDir(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want none", entries)
	}
}

func TestScanMirrorSkipsEmptyDirAndScansNext(t *testing.T) {
	proj := t.TempDir()
	dest := t.TempDir()
	testutil.WriteFile(t, proj, "tui.json", `{"theme":"dark"}`)

	entries, err := ScanMirror("opencode*.json*", nil, "", proj, dest)
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	if len(entries) != 1 || entries[0].HostPath != filepath.Join(proj, "tui.json") {
		t.Fatalf("entries = %+v, want the project file", entries)
	}
}

func TestScanMirrorSortsEntriesByVMPath(t *testing.T) {
	user := t.TempDir()
	dest := t.TempDir()
	testutil.WriteFile(t, user, "b.json", `{}`)
	testutil.WriteFile(t, user, "a.json", `{}`)
	testutil.WriteFile(t, user, "c.json", `{}`)

	// A removed sort leaves map-iteration order, so repeat to make the
	// non-determinism observable rather than a coin flip.
	for range 50 {
		entries, err := ScanMirror("opencode*.json*", nil, user, "", dest)
		if err != nil {
			t.Fatalf("ScanMirror: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("entries = %d, want 3", len(entries))
		}
		for i := 1; i < len(entries); i++ {
			if entries[i-1].VMPath > entries[i].VMPath {
				t.Fatalf("entries not sorted by VMPath: %q > %q", entries[i-1].VMPath, entries[i].VMPath)
			}
		}
	}
}

func TestScanMirrorDescendsIntoDirectoryMatchingPattern(t *testing.T) {
	user := t.TempDir()
	dest := t.TempDir()
	matchingDir := filepath.Join(user, "opencode-dir.json")
	if err := os.MkdirAll(matchingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, matchingDir, "inner.txt", "inner")

	entries, err := ScanMirror("opencode*.json*", nil, user, "", dest)
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	want := filepath.Join(dest, "opencode-dir.json", "inner.txt")
	if len(entries) != 1 || entries[0].VMPath != want {
		t.Fatalf("entries = %+v, want the nested file %q", entries, want)
	}
}

func TestScanMirrorMirrorsNestedFileMatchingPattern(t *testing.T) {
	user := t.TempDir()
	dest := t.TempDir()
	sub := filepath.Join(user, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sub, "opencode-x.json", `{"x":1}`)

	entries, err := ScanMirror("opencode*.json*", nil, user, "", dest)
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	want := filepath.Join(dest, "sub", "opencode-x.json")
	if len(entries) != 1 || entries[0].VMPath != want {
		t.Fatalf("entries = %+v, want the nested file %q", entries, want)
	}
}

func TestScanMirrorSkipsFamilyFileWithoutPatternMatch(t *testing.T) {
	user := t.TempDir()
	dest := t.TempDir()
	testutil.WriteFile(t, user, "tui.json", `{"theme":"dark"}`)

	entries, err := ScanMirror("opencode*.json*", []string{"tui.json"}, user, "", dest)
	if err != nil {
		t.Fatalf("ScanMirror: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none (tui.json is reserved by the family)", entries)
	}
}

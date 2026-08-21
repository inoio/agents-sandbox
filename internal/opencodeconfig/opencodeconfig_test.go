package opencodeconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func writeSnippet(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, name, content)
}

func TestIsJSONFile(t *testing.T) {
	cases := map[string]bool{
		"opencode.json":  true,
		"opencode.jsonc": true,
		"opencode.json5": true,
		"auth.json":      true,
		"tui.json5":      true,
		"README.md":      false,
		"opencode.txt":   false,
		"opencode":       false,
	}
	for name, want := range cases {
		if got := isJSONFile(name); got != want {
			t.Errorf("isJSONFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBuildOpenCodeJSONSourcesInMergeOrder(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeSnippet(t, user, "02-user.json", `{"a": 1}`)
	writeSnippet(t, user, "01-user.json", `{"a": 2}`)
	writeSnippet(t, proj, "proj.json", `{"b": 3}`)

	_, sources, has, err := BuildOpenCodeJSON(user, proj)
	if err != nil || !has {
		t.Fatalf("BuildOpenCodeJSON: has=%v err=%v", has, err)
	}
	want := []string{
		filepath.Join(user, "01-user.json"),
		filepath.Join(user, "02-user.json"),
		filepath.Join(proj, "proj.json"),
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

func TestBuildOpenCodeJSONNoSnippets(t *testing.T) {
	data, sources, has, err := BuildOpenCodeJSON(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("BuildOpenCodeJSON: %v", err)
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

func TestBuildOpenCodeJSONEmitsOpenCodeJSON(t *testing.T) {
	user := t.TempDir()
	writeSnippet(t, user, "opencode.json5", `{"model": "x", "instructions": "be brief"}`)

	data, sources, has, err := BuildOpenCodeJSON(user, "")
	if err != nil {
		t.Fatalf("BuildOpenCodeJSON: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if len(sources) != 1 || sources[0] != filepath.Join(user, "opencode.json5") {
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

func TestBuildOpenCodeJSONDeterministic(t *testing.T) {
	user := t.TempDir()
	writeSnippet(t, user, "a.json", `{"b": 1, "a": 2}`)
	writeSnippet(t, user, "c.json5", `{"c": {"z": 1, "y": 2}}`)

	var first []byte
	for range 5 {
		data, _, has, err := BuildOpenCodeJSON(user, "")
		if err != nil || !has {
			t.Fatalf("BuildOpenCodeJSON: has=%v err=%v", has, err)
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

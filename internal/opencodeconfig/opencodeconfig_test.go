package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSnippet(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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

func TestScanSnippetFilesAlphabeticalLaterWins(t *testing.T) {
	dir := t.TempDir()
	// "01-model.json5" runs before "02-agent.jsonc"; later scalar wins.
	writeSnippet(t, dir, "01-model.json5", `{"model": "first", "theme": "dark"}`)
	writeSnippet(t, dir, "02-agent.jsonc", `{"model": "second", "agent": "builder"}`)

	merged := scanSnippetFiles(dir)
	if merged["model"] != "second" {
		t.Errorf("expect later file to override scalar, got %v", merged["model"])
	}
	if merged["theme"] != "dark" {
		t.Errorf("expect earlier file's disjoint key kept, got %v", merged["theme"])
	}
	if merged["agent"] != "builder" {
		t.Errorf("expect later file's key kept, got %v", merged["agent"])
	}
}

func TestScanSnippetFilesDeepMergeMaps(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "a.json", `{"permission": {"read": {"*": "allow", "*.env": "deny"}}}`)
	writeSnippet(t, dir, "b.json", `{"permission": {"read": {"external_directory": "allow"}}}`)

	merged := scanSnippetFiles(dir)
	perm := merged["permission"].(map[string]any)
	read := perm["read"].(map[string]any)
	if read["*"] != "allow" || read["external_directory"] != "allow" {
		t.Errorf("expected recursive map merge, got %v", read)
	}
}

func TestScanSnippetFilesUserThenProjectOrder(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeSnippet(t, user, "cfg.json", `{"val": "user"}`)
	writeSnippet(t, proj, "cfg.json", `{"val": "project"}`)

	merged := scanSnippetFiles(user, proj)
	if merged["val"] != "project" {
		t.Errorf("expected project to override user, got %v", merged["val"])
	}
}

func TestBuildOpenCodeJSONNoSnippets(t *testing.T) {
	data, has, err := BuildOpenCodeJSON(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("BuildOpenCodeJSON: %v", err)
	}
	if has {
		t.Error("expected has=false when no snippet files exist")
	}
	if data != nil {
		t.Errorf("expected nil bytes when no snippets, got %q", data)
	}
}

func TestBuildOpenCodeJSONEmitsOpenCodeJSON(t *testing.T) {
	user := t.TempDir()
	writeSnippet(t, user, "opencode.json5", `{"model": "x", "instructions": "be brief"}`)

	data, has, err := BuildOpenCodeJSON(user, "")
	if err != nil {
		t.Fatalf("BuildOpenCodeJSON: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
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
		data, has, err := BuildOpenCodeJSON(user, "")
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

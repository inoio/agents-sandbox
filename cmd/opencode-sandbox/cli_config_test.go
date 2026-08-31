package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestConfigShowPrintsMergedConfig(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "show")
	snippetPath := filepath.Join(configpaths.Get().UserOpencodeConfigDir(), "opencode-x.json5")
	testutil.WritePath(
		t,
		snippetPath,
		`{"model":"x","instructions":"be brief"}`,
	)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config show: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, `"model": "x"`) {
		t.Errorf("expected merged model in output, got:\n%s", joined)
	}
	if !strings.Contains(joined, "merged files:") {
		t.Errorf("expected merged files listing header, got:\n%s", joined)
	}
	if !strings.Contains(joined, snippetPath) {
		t.Errorf("expected merged source path %q in output, got:\n%s", snippetPath, joined)
	}
}

func TestConfigHomeListsMappings(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "home")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "home.yaml", ".gitconfig:\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config home: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "/home/dev/.gitconfig") {
		t.Errorf("expected home target in output, got:\n%s", joined)
	}
}

func TestConfigHomeNotFoundManifest(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "home")
	if err := os.MkdirAll(configpaths.Get().UserConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config home: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "No home.yaml manifest found.") {
		t.Errorf("expected not-found message, got:\n%s", joined)
	}
}

func TestConfigHomeEmptyManifest(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "home")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "home.yaml", "")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config home: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "No home.yaml mappings.") {
		t.Errorf("expected empty-manifest message, got:\n%s", joined)
	}
	if strings.Contains(joined, "No home.yaml manifest found.") {
		t.Errorf("found misleading not-found message for an existing empty manifest:\n%s", joined)
	}
}

func TestConfigShowNoSnippetFiles(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "show")
	if err := os.MkdirAll(configpaths.Get().UserOpencodeConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config show: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "No opencode snippet files found") {
		t.Errorf("expected no-snippets message, got:\n%s", joined)
	}
}

func TestConfigHomeManifestError(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "home")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "home.yaml", "../escape:\n")

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for an escaping home.yaml target")
	}
	if !strings.Contains(err.Error(), "escapes the home directory") {
		t.Errorf("expected 'escapes the home directory' error, got: %v", err)
	}
}

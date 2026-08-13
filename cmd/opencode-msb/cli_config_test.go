package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigShowPrintsMergedConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	configpaths.WithRealConfigPaths(t)
	writeFile(
		t,
		filepath.Join(base, "opencode-msb", "opencode", "opencode.json5"),
		`{"model":"x","instructions":"be brief"}`,
	)

	ui := testutil.TermUIMock(t)
	root := buildRootCmd(&ui)
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	root.SetArgs([]string{"config", "show"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config show: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, `"model": "x"`) {
		t.Errorf("expected merged model in output, got:\n%s", joined)
	}
}

func TestConfigHomeListsMappings(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	configpaths.WithRealConfigPaths(t)
	cfgDir := filepath.Join(base, "opencode-msb")
	writeFile(t, filepath.Join(cfgDir, "home.yaml"), ".gitconfig:\n")

	ui := testutil.TermUIMock(t)
	root := buildRootCmd(&ui)
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	root.SetArgs([]string{"config", "home"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config home: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "/home/dev/.gitconfig") {
		t.Errorf("expected home target in output, got:\n%s", joined)
	}
}

func TestConfigHomeNotFoundManifest(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	configpaths.WithRealConfigPaths(t)
	cfgDir := filepath.Join(base, "opencode-msb")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ui := testutil.TermUIMock(t)
	root := buildRootCmd(&ui)
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	root.SetArgs([]string{"config", "home"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config home: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "No home.yaml manifest found.") {
		t.Errorf("expected not-found message, got:\n%s", joined)
	}
}

func TestConfigHomeEmptyManifest(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	configpaths.WithRealConfigPaths(t)
	cfgDir := filepath.Join(base, "opencode-msb")
	writeFile(t, filepath.Join(cfgDir, "home.yaml"), "")

	ui := testutil.TermUIMock(t)
	root := buildRootCmd(&ui)
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	root.SetArgs([]string{"config", "home"})
	if err := root.Execute(); err != nil {
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

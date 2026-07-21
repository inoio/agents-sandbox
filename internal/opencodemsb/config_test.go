package opencodemsb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProviderConfigParsesJSON5(t *testing.T) {
	cfg, err := LoadProviderConfig(EmbeddedProviderConfig)
	if err != nil {
		t.Fatalf("LoadProviderConfig failed: %v", err)
	}
	litellm, ok := cfg["provider"].(map[string]any)["litellm"].(map[string]any)
	if !ok {
		t.Fatal("expected provider.litellm to exist")
	}
	if litellm["name"] != "LiteLLM" {
		t.Errorf("expected name=LiteLLM, got %v", litellm["name"])
	}
}

func TestDeepMergeNested(t *testing.T) {
	base := map[string]any{
		"a": map[string]any{"x": 1, "y": 2},
		"b": "keep",
	}
	override := map[string]any{
		"a": map[string]any{"y": 99, "z": 3},
		"c": "new",
	}
	result := DeepMerge(base, override)
	a := result["a"].(map[string]any)
	if a["x"] != 1 {
		t.Errorf("expected x=1 (from base), got %v", a["x"])
	}
	if a["y"] != 99 {
		t.Errorf("expected y=99 (overridden), got %v", a["y"])
	}
	if a["z"] != 3 {
		t.Errorf("expected z=3 (new), got %v", a["z"])
	}
	if result["b"] != "keep" {
		t.Errorf("expected b='keep', got %v", result["b"])
	}
	if result["c"] != "new" {
		t.Errorf("expected c='new', got %v", result["c"])
	}
}

func TestBuildMergedConfigCreatesOpencodeJsoncWhenAbsent(t *testing.T) {
	providerCfg := map[string]any{
		"provider": map[string]any{"litellm": map[string]any{"name": "LiteLLM"}},
	}
	files, err := BuildMergedConfig("", "", providerCfg)
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	data, ok := files["opencode.jsonc"]
	if !ok {
		t.Fatal("expected opencode.jsonc to be created")
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("opencode.jsonc is not valid JSON: %v", err)
	}
	if _, ok := parsed["provider"]; !ok {
		t.Error("expected provider key in opencode.jsonc")
	}
}

func TestBuildMergedConfigMergesUserOpencodeJsonc(t *testing.T) {
	tmp := t.TempDir()
	userCfg := map[string]any{"theme": "dark"}
	userBytes, _ := json.Marshal(userCfg)
	os.WriteFile(filepath.Join(tmp, "opencode.jsonc"), userBytes, 0o644)

	providerCfg := map[string]any{
		"provider": map[string]any{"litellm": map[string]any{"name": "LiteLLM"}},
	}
	files, err := BuildMergedConfig(tmp, "", providerCfg)
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	data := files["opencode.jsonc"]
	var parsed map[string]any
	json.Unmarshal(data, &parsed)
	if parsed["theme"] != "dark" {
		t.Errorf("expected theme=dark to be preserved, got %v", parsed["theme"])
	}
	if _, ok := parsed["provider"]; !ok {
		t.Error("expected provider to be merged in")
	}
}

func TestBuildMergedConfigCopiesNonJsonFiles(t *testing.T) {
	userDir := t.TempDir()
	os.WriteFile(filepath.Join(userDir, "instructions.txt"), []byte("hello"), 0o644)

	files, err := BuildMergedConfig(userDir, "", map[string]any{})
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	if string(files["instructions.txt"]) != "hello" {
		t.Errorf("expected instructions.txt content 'hello', got %q", files["instructions.txt"])
	}
}

func TestBuildMergedConfigProjectDirOverridesUserDir(t *testing.T) {
	userDir := t.TempDir()
	projectDir := t.TempDir()
	userBytes, _ := json.Marshal(map[string]any{"val": "user"})
	projBytes, _ := json.Marshal(map[string]any{"val": "project"})
	os.WriteFile(filepath.Join(userDir, "opencode.jsonc"), userBytes, 0o644)
	os.WriteFile(filepath.Join(projectDir, "opencode.jsonc"), projBytes, 0o644)

	files, err := BuildMergedConfig(userDir, projectDir, map[string]any{})
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal(files["opencode.jsonc"], &parsed)
	if parsed["val"] != "project" {
		t.Errorf("expected project override, got %v", parsed["val"])
	}
}

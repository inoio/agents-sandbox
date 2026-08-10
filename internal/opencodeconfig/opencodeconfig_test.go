package config

import (
	"bytes"
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
	result := deepMerge(base, override)
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
	if err := os.WriteFile(filepath.Join(tmp, "opencode.jsonc"), userBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	providerCfg := map[string]any{
		"provider": map[string]any{"litellm": map[string]any{"name": "LiteLLM"}},
	}
	files, err := BuildMergedConfig(tmp, "", providerCfg)
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	data := files["opencode.jsonc"]
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["theme"] != "dark" {
		t.Errorf("expected theme=dark to be preserved, got %v", parsed["theme"])
	}
	if _, ok := parsed["provider"]; !ok {
		t.Error("expected provider to be merged in")
	}
}

func TestBuildMergedConfigCopiesNonJsonFiles(t *testing.T) {
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDir, "instructions.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	if err := os.WriteFile(filepath.Join(userDir, "opencode.jsonc"), userBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "opencode.jsonc"), projBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := BuildMergedConfig(userDir, projectDir, map[string]any{})
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(files["opencode.jsonc"], &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["val"] != "project" {
		t.Errorf("expected project override, got %v", parsed["val"])
	}
}

func TestBuildMergedConfigPreservesEmbeddedNonProviderFields(t *testing.T) {
	embeddedCfg := map[string]any{
		"permission": map[string]any{"read": map[string]any{"*": "allow"}},
		"provider":   map[string]any{"litellm": map[string]any{"name": "LiteLLM"}},
	}

	files, err := BuildMergedConfig("", "", embeddedCfg)
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	data := files["opencode.jsonc"]
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if _, ok := parsed["permission"]; !ok {
		t.Error("expected permission from embedded config, missing from output")
	}

	permMap, ok := parsed["permission"].(map[string]any)
	if !ok {
		t.Fatalf("expected permission to be a map, got %T", parsed["permission"])
	}
	readMap := permMap["read"].(map[string]any)
	if readMap["*"] != "allow" {
		t.Errorf("expected permission.read.*=allow, got %v", readMap["*"])
	}
}

func TestBuildMergedConfigUserOverridesEmbedded(t *testing.T) {
	tmp := t.TempDir()
	embeddedCfg := map[string]any{
		"model":      "base-model",
		"permission": map[string]any{"read": map[string]any{"*": "deny"}},
		"provider":   map[string]any{"litellm": map[string]any{"name": "base"}},
	}
	userCfg := map[string]any{"model": "user-model", "theme": "dark"}
	userBytes, _ := json.Marshal(userCfg)
	if err := os.WriteFile(filepath.Join(tmp, "opencode.jsonc"), userBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := BuildMergedConfig(tmp, "", embeddedCfg)
	if err != nil {
		t.Fatalf("BuildMergedConfig failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(files["opencode.jsonc"], &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed["model"] != "user-model" {
		t.Errorf("expected model=user-model (user override), got %v", parsed["model"])
	}
	if parsed["theme"] != "dark" {
		t.Errorf("expected theme=dark, got %v", parsed["theme"])
	}

	permMap := parsed["permission"].(map[string]any)
	if readMap := permMap["read"].(map[string]any); readMap["*"] != "deny" {
		t.Errorf("expected permission.read.*=deny (from embedded), got %v", readMap["*"])
	}
}

func TestBuildMergedConfigDeterministic(t *testing.T) {
	providerCfg := map[string]any{
		"permission": map[string]any{"read": map[string]any{"*": "allow", "*.env": "deny"}},
		"provider": map[string]any{
			"litellm": map[string]any{
				"name": "LiteLLM",
				"models": map[string]any{
					"gpt-4": map[string]any{"name": "GPT 4"},
				},
			},
		},
		"model": "base-model",
	}
	tmp := t.TempDir()
	userCfg := map[string]any{"theme": "dark", "instructions": "talk concise"}
	userBytes, _ := json.Marshal(userCfg)
	_ = os.WriteFile(filepath.Join(tmp, "opencode.jsonc"), userBytes, 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "instructions.md"), []byte("be nice"), 0o644)

	for _, scenario := range []struct {
		name    string
		userDir string
		projDir string
		cfg     map[string]any
	}{
		{"no user, no project", "", "", providerCfg},
		{"user only", tmp, "", providerCfg},
		{"project only", "", tmp, map[string]any{}},
		{"user + project", tmp, tmp, providerCfg},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Map[file_key][]iteration_index_in_first_results
			firstResults := make(map[string][][]byte)
			for range 5 {
				files, err := BuildMergedConfig(scenario.userDir, scenario.projDir, scenario.cfg)
				if err != nil {
					t.Fatalf("BuildMergedConfig failed: %v", err)
				}
				for fname, data := range files {
					if _, ok := firstResults[fname]; !ok {
						firstResults[fname] = [][]byte{data}
					} else {
						firstResults[fname] = append(firstResults[fname], data)
					}
				}
			}
			for fname, iterations := range firstResults {
				for i := 1; i < len(iterations); i++ {
					if !bytes.Equal(iterations[0], iterations[i]) {
						t.Errorf("file %q: iteration 0 and %d differ (non-deterministic)", fname, i)
					}
				}
			}
		})
	}
}

package configfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetStringPreservesYAMLCommentsAndOrder(t *testing.T) {
	config, err := Parse("config.yaml", []byte("# launcher settings\n"+
		"upgrade: never\n"+
		"network:\n"+
		"  profile: none # deny by default\n"+
		"agent: opencode\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	config.SetString([]string{"home", ".config/tool/rc"}, "tool/rc")
	updated, err := config.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, comment := range []string{"# launcher settings", "# deny by default"} {
		if !strings.Contains(string(updated), comment) {
			t.Errorf("updated config lost comment %q:\n%s", comment, updated)
		}
	}
	if strings.Index(string(updated), "upgrade:") > strings.Index(string(updated), "network:") ||
		strings.Index(string(updated), "network:") > strings.Index(string(updated), "agent:") {
		t.Errorf("updated config changed the key order:\n%s", updated)
	}
}

func TestJSONFamilyRoundTrip(t *testing.T) {
	tests := map[string]string{
		"config.json":  `{"network":{"egress-allow":["a.test"]}}`,
		"config.jsonc": "{network: {'egress-allow': ['a.test']}, // comment\n}",
		"config.json5": `{"network":{"egress-allow":["a.test"],},}`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			config, err := Parse(name, []byte(data))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			config.AppendListItems([]string{"network", "egress-allow"}, []string{"b.test"})
			updated, err := config.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(updated, &decoded); err != nil {
				t.Fatalf("updated config is not valid JSON: %v\n%s", err, updated)
			}
			network, _ := decoded["network"].(map[string]any)
			hosts, _ := network["egress-allow"].([]any)
			if len(hosts) != 2 {
				t.Errorf("egress-allow = %v, want two hosts", hosts)
			}
		})
	}
}

func TestAppendListItemsPreservesYAMLSequenceComments(t *testing.T) {
	config, err := Parse(
		"config.yaml",
		[]byte("network:\n  egress-allow:\n    # needed for the docs\n    - existing.test\n"),
	)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	config.AppendListItems([]string{"network", "egress-allow"}, []string{"new.test"})
	updated, err := config.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(updated), "# needed for the docs") {
		t.Errorf("updated config lost the sequence comment:\n%s", updated)
	}
	if strings.Index(string(updated), "existing.test") > strings.Index(string(updated), "new.test") {
		t.Errorf("new host was not appended after the existing ones:\n%s", updated)
	}
}

func TestFilterListItemsPreservesYAMLSequenceComments(t *testing.T) {
	config, err := Parse("config.yaml", []byte("network:\n  egress-deny:\n    - a.test\n    # keep me\n    - b.test\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	config.FilterListItems([]string{"network", "egress-deny"}, func(item string) bool { return item != "a.test" })
	updated, err := config.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(updated), "# keep me") || !strings.Contains(string(updated), "- b.test") {
		t.Errorf("updated config =\n%s\nwant the comment and b.test kept", updated)
	}
	if strings.Contains(string(updated), "a.test") {
		t.Errorf("updated config still contains the filtered item:\n%s", updated)
	}
}

func TestLoadDirFindsConfigInOrder(t *testing.T) {
	dir := t.TempDir()
	if path, data, err := LoadDir(
		dir,
	); err != nil || path != filepath.Join(dir, "config.yaml") ||
		string(data) != "{}\n" {
		t.Fatalf("LoadDir() = %q, %q, %v; want %q, \"{}\\n\"", path, data, err, filepath.Join(dir, "config.yaml"))
	}
	jsonPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(jsonPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if path, _, err := LoadDir(dir); err != nil || path != jsonPath {
		t.Fatalf("LoadDir() path = %q, %v; want %q", path, err, jsonPath)
	}
}

func TestGetBoolAndGetInt(t *testing.T) {
	config, err := Parse("config.yaml", []byte("dind: true\ncpus: 16\nnested:\n  flag: false\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, ok := config.GetBool([]string{"dind"}); !ok || !got {
		t.Errorf("GetBool dind = %v, %v; want true, true", got, ok)
	}
	if got, ok := config.GetBool([]string{"nested", "flag"}); !ok || got {
		t.Errorf("GetBool nested.flag = %v, %v; want false, true", got, ok)
	}
	if _, ok := config.GetBool([]string{"cpus"}); ok {
		t.Error("GetBool reported an integer as a boolean")
	}
	if _, ok := config.GetBool([]string{"absent"}); ok {
		t.Error("GetBool reported an absent key as present")
	}
	if got, ok := config.GetInt([]string{"cpus"}); !ok || got != 16 {
		t.Errorf("GetInt cpus = %d, %v; want 16, true", got, ok)
	}
	if _, ok := config.GetInt([]string{"dind"}); ok {
		t.Error("GetInt reported a boolean as an integer")
	}
	if _, ok := config.GetInt([]string{"absent"}); ok {
		t.Error("GetInt reported an absent key as present")
	}
}

func TestGetIntReadsJSONNumbers(t *testing.T) {
	config, err := Parse("config.json", []byte(`{"cpus": 16}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, ok := config.GetInt([]string{"cpus"}); !ok || got != 16 {
		t.Errorf("GetInt cpus = %d, %v; want 16, true", got, ok)
	}
}

func TestGetStringOnNestedPath(t *testing.T) {
	config, err := Parse("config.yaml", []byte("network:\n  profile: none\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, ok := config.GetString([]string{"network", "profile"}); !ok || got != "none" {
		t.Errorf("GetString = %q, %v; want none, true", got, ok)
	}
	if _, ok := config.GetString([]string{"network", "egress-allow"}); ok {
		t.Error("GetString reported an absent key as present")
	}
	if _, ok := config.GetString([]string{"absent", "path"}); ok {
		t.Error("GetString reported an absent nested path as present")
	}
}

func TestSetIntAndSetBoolWriteYAMLScalars(t *testing.T) {
	config, err := Parse("config.yaml", []byte("agent: pi\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	config.SetBool([]string{"provision-host-config"}, true)
	config.SetInt([]string{"cpus"}, 4)
	updated, err := config.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := "agent: pi\nprovision-host-config: true\ncpus: 4\n"; string(updated) != want {
		t.Errorf("updated config =\n%s\nwant\n%s", updated, want)
	}
}

func TestParseRejectsNonMappingYAML(t *testing.T) {
	if _, err := Parse("config.yaml", []byte("- a.test\n")); err == nil {
		t.Fatal("Parse succeeded for a YAML sequence, want an error")
	}
}

func TestSetStringStartsEmptyYAMLFile(t *testing.T) {
	for name, data := range map[string]string{"empty file": "", "null document": "~\n"} {
		t.Run(name, func(t *testing.T) {
			config, err := Parse("config.yaml", []byte(data))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			config.SetString([]string{"home", ".config/tool/rc"}, "tool/rc")
			updated, err := config.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if want := "home:\n  .config/tool/rc: tool/rc\n"; string(updated) != want {
				t.Errorf("updated config =\n%s\nwant\n%s", updated, want)
			}
		})
	}
}

func TestFilterListItemsReplacesScalarWithSequence(t *testing.T) {
	config, err := Parse("config.yaml", []byte("network:\n  egress-deny: \"a.test, b.test\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	config.FilterListItems([]string{"network", "egress-deny"}, func(item string) bool { return item != "a.test" })
	updated, err := config.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(updated), "a.test") || !strings.Contains(string(updated), "- b.test") {
		t.Errorf("updated config =\n%s\nwant a.test removed and b.test kept", updated)
	}
}

func TestMarshalReportsJSONError(t *testing.T) {
	config, err := Parse("config.json", []byte("{}"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	marshalErr := errors.New("marshal failed")
	originalMarshal := marshalJSONIndentFn
	t.Cleanup(func() { marshalJSONIndentFn = originalMarshal })
	marshalJSONIndentFn = func(any, string, string) ([]byte, error) { return nil, marshalErr }
	if _, err := config.Marshal(); !errors.Is(err, marshalErr) {
		t.Errorf("Marshal error = %v, want %v", err, marshalErr)
	}
}

type failingYAMLMarshaler struct{}

var errYAMLMarshal = errors.New("yaml marshal failed")

func (failingYAMLMarshaler) MarshalYAML() (any, error) { return nil, errYAMLMarshal }

func TestMarshalYAMLReportsEncodeError(t *testing.T) {
	if _, err := marshalYAML(failingYAMLMarshaler{}); !errors.Is(err, errYAMLMarshal) {
		t.Errorf("marshalYAML error = %v, want %v", err, errYAMLMarshal)
	}
}

func TestWriteAtomicWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	if err := WriteAtomic(path, []byte("agent: pi\n"), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "agent: pi\n" {
		t.Fatalf("read back = %q, %v", data, err)
	}
}

func TestCommitTempFileReportsErrorsOfClosedFile(t *testing.T) {
	dir := t.TempDir()
	file, err := os.CreateTemp(dir, "closed-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if err := commitTempFile(file, filepath.Join(dir, "out.yaml"), []byte("x"), 0o600); !errors.Is(err, os.ErrClosed) {
		t.Errorf("error = %v, want %v", err, os.ErrClosed)
	}
}

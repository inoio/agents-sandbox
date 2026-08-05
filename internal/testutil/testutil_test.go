package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func TestNewTestio_ReturnsEmptyMock(t *testing.T) {
	mock := NewTestio(t)
	if mock.InfoCalls != nil {
		t.Error("InfoCalls should be nil for empty mock")
	}
	if mock.WarnCalls != nil {
		t.Error("WarnCalls should be nil for empty mock")
	}
	if mock.ErrorCalls != nil {
		t.Error("ErrorCalls should be nil for empty mock")
	}
}

func TestNewTestio_MockRecordsCalls(t *testing.T) {
	mock := NewTestio(t)

	mock.Info("hello")
	mock.Infof("fmt %s", "args")
	mock.Warn("warning")
	mock.Errorf("error %d", 42)
	mock.Verbose("shh")
	mock.Out("output")
	mock.Spinner("spin")
	mock.Spinnerf("spin %s", "1")
	_, _ = mock.Select("choose", []termio.Choice{{Key: "a"}}, "a")
	_, _ = mock.ConfirmDefault("yes?", true)
	_, _ = mock.Input("type?", "default")

	if n := len(mock.InfoCalls); n != 2 {
		t.Errorf("expected 2 InfoCalls, got %d: %v", n, mock.InfoCalls)
	}
	if len(mock.WarnCalls) != 1 {
		t.Errorf("expected 1 WarnCall, got %d", len(mock.WarnCalls))
	}
	if len(mock.ErrorCalls) != 1 {
		t.Errorf("expected 1 ErrorCall, got %d", len(mock.ErrorCalls))
	}
	if mock.ErrorCalls[0].Err != nil {
		t.Error("Errorf with nil err should not set Err field")
	}
	if len(mock.VerboseCalls) != 1 {
		t.Errorf("expected 1 VerboseCall, got %d", len(mock.VerboseCalls))
	}
	if len(mock.OutCalls) != 1 {
		t.Errorf("expected 1 OutCall, got %d", len(mock.OutCalls))
	}
	if len(mock.SpinnerCalls) != 2 {
		t.Errorf("expected 2 SpinnerCalls, got %d", len(mock.SpinnerCalls))
	}
}

func TestNewTestio_StdoutStderrAreWriters(t *testing.T) {
	mock := NewTestio(t)

	n, err := mock.StdOut().Write([]byte("out"))
	if err != nil {
		t.Fatalf("StdOut().Write: %v", err)
	}
	if n != 3 {
		t.Errorf("wrote %d bytes, want 3", n)
	}
	if got := mock.StdOutBuffer.String(); got != "out" {
		t.Errorf("StdOutBuffer = %q, want %q", got, "out")
	}

	n, err = mock.StdErr().Write([]byte("err"))
	if err != nil {
		t.Fatalf("StdErr().Write: %v", err)
	}
	if n != 3 {
		t.Errorf("wrote %d bytes, want 3", n)
	}
	if got := mock.StdErrBuffer.String(); got != "err" {
		t.Errorf("StdErrBuffer = %q, want %q", got, "err")
	}
}

func TestNewTestio_SelectReturnsDefault(t *testing.T) {
	mock := NewTestio(t)
	got, err := mock.Select("prompt", []termio.Choice{{Key: "a"}, {Key: "b"}}, "b")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got != "b" {
		t.Errorf("Select = %q, want %q", got, "b")
	}
}

func TestNewTestio_ConfirmDefaultReturnsDefault(t *testing.T) {
	mock := NewTestio(t)
	got, err := mock.ConfirmDefault("prompt", true)
	if err != nil {
		t.Fatalf("ConfirmDefault: %v", err)
	}
	if !got {
		t.Error("ConfirmDefault with true default should return true")
	}
}

func TestNewTestio_InputReturnsDefault(t *testing.T) {
	mock := NewTestio(t)
	got, err := mock.Input("prompt", "hello")
	if err != nil {
		t.Fatalf("Input: %v", err)
	}
	if got != "hello" {
		t.Errorf("Input = %q, want %q", got, "hello")
	}
}

func TestNewTestio_CustomFnOverrides(t *testing.T) {
	mock := NewTestio(t)

	mock.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "custom", nil
	}
	got, _ := mock.Select("", nil, "")
	if got != "custom" {
		t.Errorf("SelectFn override = %q, want %q", got, "custom")
	}

	mock.ConfirmDefaultFn = func(_ string, _ bool) (bool, error) {
		return false, nil
	}
	got2, _ := mock.ConfirmDefault("", true)
	if got2 {
		t.Error("ConfirmDefaultFn override should return false")
	}

	mock.InputFn = func(_ string, _ string) (string, error) {
		return "typed", nil
	}
	got3, _ := mock.Input("", "")
	if got3 != "typed" {
		t.Errorf("InputFn override = %q, want %q", got3, "typed")
	}
}

func TestWriteFile_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	WriteFile(t, dir, "hello.txt", "world")

	got, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "world" {
		t.Errorf("file content = %q, want %q", got, "world")
	}
}

func TestWriteFile_SetsPermissions(t *testing.T) {
	dir := t.TempDir()
	WriteFile(t, dir, "file.txt", "content")

	info, err := os.Stat(filepath.Join(dir, "file.txt"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	want := os.FileMode(0o600)
	if info.Mode().Perm() != want {
		t.Errorf("file permission = %o, want %o", info.Mode().Perm(), want)
	}
}

func TestWriteFile_CreatesOverwrite(t *testing.T) {
	dir := t.TempDir()
	WriteFile(t, dir, "file.txt", "original")
	WriteFile(t, dir, "file.txt", "overwritten")

	got, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "overwritten" {
		t.Errorf("file content = %q, want %q", got, "overwritten")
	}
}

func TestWritePath_WritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absolute.txt")
	WritePath(t, path, "direct content")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "direct content" {
		t.Errorf("file content = %q, want %q", got, "direct content")
	}
}

func TestWritePath_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")

	_ = os.WriteFile(path, []byte("old"), 0o600)
	WritePath(t, path, "new")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("file content = %q, want %q", got, "new")
	}
}

func TestWriteYAML_WritesMap(t *testing.T) {
	dir := t.TempDir()
	WriteYAML(t, dir, "config.yaml", map[string]any{
		"name":  "test",
		"value": 42,
	})

	got, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(got)
	// YAML marshalling of map with string/float keys produces keys in sorted order.
	if !contains(content, "name: test") && !contains(content, "name:test") {
		t.Errorf("YAML content missing 'name: test':\n%s", content)
	}
	if !contains(content, "value: 42") {
		t.Errorf("YAML content missing 'value: 42':\n%s", content)
	}
}

func TestWriteYAML_MarshalsNestedMap(t *testing.T) {
	dir := t.TempDir()
	WriteYAML(t, dir, "nested.yaml", map[string]any{
		"outer": map[string]any{
			"inner": "value",
		},
	})

	got, err := os.ReadFile(filepath.Join(dir, "nested.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(got)
	if !contains(content, "inner:") {
		t.Errorf("YAML content missing nested 'inner':\n%s", content)
	}
	if !contains(content, "value") {
		t.Errorf("YAML content missing 'value':\n%s", content)
	}
}

func TestWriteYAML_MarshalsSlice(t *testing.T) {
	dir := t.TempDir()
	WriteYAML(t, dir, "slice.yaml", map[string]any{
		"items": []string{"a", "b", "c"},
	})

	got, err := os.ReadFile(filepath.Join(dir, "slice.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(got)
	if !contains(content, "items:") {
		t.Errorf("YAML content missing 'items:':\n%s", content)
	}
	if !contains(content, "- a") || !contains(content, "- b") {
		t.Errorf("YAML slice items missing:\n%s", content)
	}
}

func TestWriteYAML_ProducesValidYAML(t *testing.T) {
	dir := t.TempDir()
	WriteYAML(t, dir, "valid.yaml", map[string]any{
		"string":  "hello",
		"int":     123,
		"float":   3.14,
		"bool":    true,
		"nothing": nil,
		"list":    []int{1, 2},
	})

	b, err := os.ReadFile(filepath.Join(dir, "valid.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var result map[string]any
	if err := yaml.Unmarshal(b, &result); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	if result["string"] != "hello" {
		t.Errorf("string field = %v, want %v", result["string"], "hello")
	}
	if result["int"] != 123 {
		t.Errorf("int field = %v, want 123", result["int"])
	}
	if result["bool"] != true {
		t.Errorf("bool field = %v, want true", result["bool"])
	}
}

// contains is a small helper to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

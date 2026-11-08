package configdecode_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/titanous/json5"

	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestFormatFor pins the extension-to-format mapping, case-insensitively, and
// that an unknown extension reports ok=false.
func TestFormatFor(t *testing.T) {
	tests := []struct {
		name   string
		want   configdecode.Format
		wantOK bool
	}{
		{"config.yaml", configdecode.FormatYAML, true},
		{"config.YML", configdecode.FormatYAML, true},
		{"config.json", configdecode.FormatJSON, true},
		{"config.jsonc", configdecode.FormatJSON, true},
		{"config.json5", configdecode.FormatJSON, true},
		{"config.toml", configdecode.FormatTOML, true},
		{"config.ini", configdecode.FormatYAML, false},
		{"config", configdecode.FormatYAML, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := configdecode.FormatFor(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("FormatFor(%q) ok = %v, want %v", tc.name, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("FormatFor(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestDecodeJSON5AcceptsCommentsAndTrailingCommas pins that the shared JSON
// path is json5, so a comment and a trailing comma are accepted.
func TestDecodeJSON5AcceptsCommentsAndTrailingCommas(t *testing.T) {
	data := []byte("{\n  // comment\n  \"memory\": \"8G\",\n}")
	m, err := configdecode.Decode(configdecode.FormatJSON, data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", m["memory"])
	}
}

// TestDecodePlainYAML pins that a plain YAML document decodes through the same
// shared path.
func TestDecodePlainYAML(t *testing.T) {
	m, err := configdecode.Decode(configdecode.FormatYAML, []byte("memory: 8G\ncpus: 2\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m["memory"] != "8G" || m["cpus"] != 2 {
		t.Errorf("decoded = %v, want memory 8G and cpus 2", m)
	}
}

func TestDecodeTOML(t *testing.T) {
	m, err := configdecode.Decode(configdecode.FormatTOML, []byte("memory = \"8G\"\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", m["memory"])
	}
}

// TestDecodeReportsAParseFailure pins that a JSON parse failure surfaces the
// json5 syntax error.
func TestDecodeReportsAParseFailure(t *testing.T) {
	_, err := configdecode.Decode(configdecode.FormatJSON, []byte("{"))
	if err == nil {
		t.Fatal("Decode = nil error, want a parse failure")
	}
	if _, ok := errors.AsType[*json5.SyntaxError](err); !ok {
		t.Errorf("err = %v, want a wrapped *json5.SyntaxError", err)
	}
}

func TestDecodeFileReadsAJSON5FileWithComments(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.json5", "{\n  // c\n  \"memory\": \"8G\",\n}")
	m, err := configdecode.DecodeFile(filepath.Join(dir, "config.json5"), "config")
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if m["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", m["memory"])
	}
}

// TestDecodeFileReportsAReadFailure pins the read-error message shape and that
// the *fs.PathError is preserved for errors.AsType.
func TestDecodeFileReportsAReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	_, err := configdecode.DecodeFile(path, "config")
	if err == nil {
		t.Fatal("DecodeFile = nil error, want the read failure")
	}
	if !strings.Contains(err.Error(), "read config "+path) {
		t.Errorf("err = %q, want it to start the read message with the label and path", err)
	}
	if _, ok := errors.AsType[*fs.PathError](err); !ok {
		t.Errorf("err = %v, want a wrapped *fs.PathError", err)
	}
}

func TestDecodeFileReportsAYAMLFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	testutil.WriteFile(t, dir, "config.yaml", "memory: [\n")

	_, err := configdecode.DecodeFile(path, "launcher config")
	if err == nil {
		t.Fatal("DecodeFile = nil error, want a parse failure")
	}
	for _, want := range []string{path, "contains invalid YAML", "line"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("err = %v, want the YAML cause wrapped", err)
	}
}

func TestDecodeFileReportsANonYAMLFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json5")
	testutil.WriteFile(t, dir, "config.json5", `{"cpus": }`)

	_, err := configdecode.DecodeFile(path, "launcher config")
	if err == nil {
		t.Fatal("DecodeFile = nil error, want a parse failure")
	}
	if !strings.Contains(err.Error(), "parse launcher config "+path) {
		t.Errorf("err = %q, want the parse message with the label and path", err)
	}
	if _, ok := errors.AsType[*json5.SyntaxError](err); !ok {
		t.Errorf("err = %v, want a wrapped *json5.SyntaxError", err)
	}
}

func TestDecodeFileRejectsAnUnknownExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.ini")
	testutil.WriteFile(t, dir, "config.ini", "memory=8G\n")

	_, err := configdecode.DecodeFile(path, "config")
	if err == nil {
		t.Fatal("DecodeFile = nil error, want an unknown-extension failure")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %q, want it to name the path", err)
	}
}

// TestDecodeFileReadsOnlyTheApprovedBytes pins that a config file is read
// through the project config approval: a file changed since it was approved is
// refused, not decoded.
func TestDecodeFileReadsOnlyTheApprovedBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Cleanup(projectguard.Install([]string{dir}, map[string]projectguard.Approval{
		path: projectguard.ApprovalOf([]byte("memory: 4G\n")),
	}))
	testutil.WritePath(t, path, "memory: 64G\n")
	if _, err := configdecode.DecodeFile(path, "launcher config"); !errors.Is(err, projectguard.ErrNotApproved) {
		t.Errorf("DecodeFile = %v, want ErrNotApproved", err)
	}
}

// TestDecodeDataRejectsAnUnknownExtension pins that bytes read elsewhere are
// held to the same extension rule as DecodeFile.
func TestDecodeDataRejectsAnUnknownExtension(t *testing.T) {
	if _, err := configdecode.DecodeData("config.ini", "launcher config", []byte("memory: 8G\n")); err == nil {
		t.Error("DecodeData accepted an unknown extension")
	}
}

// TestFindFileRefusesAnApprovedConfigFileThatIsGone pins that a deleted
// approved config file is a refusal, not a missing layer.
func TestFindFileRefusesAnApprovedConfigFileThatIsGone(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(projectguard.Install([]string{dir}, map[string]projectguard.Approval{
		filepath.Join(dir, "config.yaml"): projectguard.ApprovalOf([]byte("network: none\n")),
	}))
	if _, _, found, err := configdecode.FindFile(dir); found || !errors.Is(err, projectguard.ErrNotApproved) {
		t.Errorf("FindFile = found %v, %v; want ErrNotApproved", found, err)
	}
	testutil.WritePath(t, filepath.Join(dir, "config.yaml"), "network: none\n")
	if path, ext, found, err := configdecode.FindFile(
		dir,
	); !found || err != nil || filepath.Base(path) != "config.yaml" ||
		ext != ".yaml" {
		t.Errorf("FindFile = %q, %q, %v, %v; want the config file", path, ext, found, err)
	}
}

// TestFindFileLooksForNothingWithoutADir pins that a layer with no dir (env,
// flags) looks for no config file and is never refused, even when an approved
// config file named like one relative to the working directory is gone.
func TestFindFileLooksForNothingWithoutADir(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(projectguard.Install(nil, map[string]projectguard.Approval{
		filepath.Join(dir, "config.yaml"): projectguard.ApprovalOf([]byte("memory: 8G\n")),
	}))
	t.Chdir(dir)
	if path, _, found, err := configdecode.FindFile(""); found || err != nil || path != "" {
		t.Errorf("FindFile(\"\") = %q, %v, %v; want nothing", path, found, err)
	}
}

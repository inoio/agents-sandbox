package homeconfig

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestLoadManifestMissingFile(t *testing.T) {
	if _, err := LoadManifest(filepath.Join(t.TempDir(), "home.yaml")); err == nil {
		t.Fatal("expected an error for a missing manifest file")
	}
}

func TestLoadManifestRejectsNonStringSource(t *testing.T) {
	dir := t.TempDir()
	writeHomeYAML(t, dir, ".x:\n  source: 42\n")
	if _, err := LoadManifest(filepath.Join(dir, "home.yaml")); err == nil {
		t.Fatal("expected an error for a non-string source")
	}
}

func TestLoadManifestRejectsNonStringHook(t *testing.T) {
	dir := t.TempDir()
	writeHomeYAML(t, dir, ".x:\n  hook: 42\n")
	if _, err := LoadManifest(filepath.Join(dir, "home.yaml")); err == nil {
		t.Fatal("expected an error for a non-string hook")
	}
}

func TestLoadManifestRejectsUnsupportedValueType(t *testing.T) {
	for _, body := range []string{".x: 42\n", ".x:\n  - a\n  - b\n"} {
		dir := t.TempDir()
		writeHomeYAML(t, dir, body)
		if _, err := LoadManifest(filepath.Join(dir, "home.yaml")); err == nil {
			t.Errorf("expected an error for value body %q", body)
		}
	}
}

func TestLoadManifestNilEntryIsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeHomeYAML(t, dir, ".x:\n")
	m, err := LoadManifest(filepath.Join(dir, "home.yaml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if e, ok := m[".x"]; !ok || e != (Entry{}) {
		t.Errorf("expected empty Entry for nil value, got %v", m[".x"])
	}
}

func TestLoadManifestUnknownTopLevelKeyType(t *testing.T) {
	// A top-level target that is not a string should still parse into the
	// manifest keyed by its string form; only the value type is validated.
	dir := t.TempDir()
	writeHomeYAML(t, dir, "42: value\n")
	m, err := LoadManifest(filepath.Join(dir, "home.yaml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if _, ok := m["42"]; !ok {
		t.Errorf("expected target 42 to be present, got %v", m)
	}
}

func TestBuildHomeFilesNoManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	files, missing, has, err := BuildHomeFiles(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFiles: %v", err)
	}
	if has {
		t.Error("expected has=false when no manifest exists")
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %v", files)
	}
	if len(missing) != 0 {
		t.Errorf("expected no missing sources, got %v", missing)
	}
}

func TestBuildHomeFilesRejectsInvalidUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, user, ".gitconfig:\n  source: [\n")
	if _, _, _, err := BuildHomeFiles(user, proj, vmHome, nil); err == nil {
		t.Fatal("expected an error for an invalid user manifest")
	}
}

func TestDescribeManifestRejectsInvalidUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, user, ".gitconfig:\n  source: [\n")
	if _, _, err := DescribeManifest(user, proj, vmHome, nil); err == nil {
		t.Fatal("expected an error for an invalid user manifest")
	}
}

func TestBuildHooksRejectsInvalidUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, user, ".gitconfig:\n  source: [\n")
	if _, err := BuildHooks(user, proj, vmHome, nil); err == nil {
		t.Fatal("expected an error for an invalid user manifest")
	}
}

func TestBuildHooksRejectsReservedTarget(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "s.sh"), "#!/bin/sh\n")
	writeHomeYAML(t, proj, ".vpn/connect.sh:\n  source: s.sh\n  hook: startup\n")
	if _, err := BuildHooks(user, proj, vmHome, []string{".vpn/connect.sh"}); err == nil {
		t.Fatal("expected an error for a reserved hook target")
	}
}

func TestShebangInterpreterMissingFile(t *testing.T) {
	if got := shebangInterpreter(filepath.Join(t.TempDir(), "nope.sh")); got != "" {
		t.Errorf("expected empty interpreter for missing file, got %q", got)
	}
}

func TestShebangInterpreterWithoutTrailingNewline(t *testing.T) {
	// Reading runs to EOF (err) rather than a newline; the interpreter is still captured.
	p := filepath.Join(t.TempDir(), "s.sh")
	testutil.WritePath(t, p, "#!/bin/bash")
	if got := shebangInterpreter(p); got != "/bin/bash" {
		t.Errorf("shebangInterpreter = %q, want %q", got, "/bin/bash")
	}
}

func TestShebangInterpreterBareMarker(t *testing.T) {
	// A shebang with only whitespace after the marker leaves an empty
	// interpreter after trimming.
	p := filepath.Join(t.TempDir(), "s.sh")
	testutil.WritePath(t, p, "#! \n")
	if got := shebangInterpreter(p); got != "" {
		t.Errorf("expected empty interpreter for bare marker, got %q", got)
	}
}

func TestBuildHomeFilesReadsUserManifest(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", user)
	testutil.WriteFile(t, user, ".gitconfig", "user=x\n")
	writeHomeYAML(t, user, ".gitconfig:\n")
	files, missing, has, err := BuildHomeFiles(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFiles: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if len(missing) != 0 {
		t.Errorf("expected no missing sources, got %v", missing)
	}
	want := "/home/dev/.gitconfig"
	if _, ok := files[want]; !ok {
		t.Errorf("expected file at %s, got %v", want, files)
	}
}

func TestErrorMessagesMentionRelevantTarget(t *testing.T) {
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{
			"empty target",
			func() error {
				_, err := ResolveVMTarget(vmHome, "", nil)
				return err
			},
			"must not be empty",
		},
		{
			"absolute target",
			func() error {
				_, err := ResolveVMTarget(vmHome, "/etc/passwd", nil)
				return err
			},
			"must be relative",
		},
		{
			"traversal",
			func() error {
				_, err := ResolveVMTarget(vmHome, "../evil", nil)
				return err
			},
			"escapes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

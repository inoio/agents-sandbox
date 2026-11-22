package reprovision

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeWritesEveryFileGroup(t *testing.T) {
	target := t.TempDir()
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte("{\n  \"model\": \"x\"\n}\n"),
		MergedPath:  opencodeMergedPath,
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig": []byte("user.name=X\n"),
		},
		Provisioned: map[string][]byte{
			"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
		},
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
		},
		Modes: map[string]os.FileMode{
			"/home/dev/.gitconfig":                0o755,
			"/home/dev/.config/opencode/tui.json": 0o600,
		},
	}

	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	cases := []struct {
		rel     string
		want    string
		wantMod os.FileMode
	}{
		{".config/opencode/opencode.jsonc", "{\n  \"model\": \"x\"\n}\n", 0o644},
		{".gitconfig", "user.name=X\n", 0o755},
		{".config/tool/cfg.toml", "k=v\n", 0o644},
		{".config/opencode/tui.json", `{"theme":"dark"}`, 0o600},
	}
	for _, tc := range cases {
		dest := filepath.Join(target, tc.rel)
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Errorf("read %s: %v", dest, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("%s = %q, want %q", dest, got, tc.want)
		}
		info, err := os.Stat(dest)
		if err != nil {
			t.Errorf("stat %s: %v", dest, err)
			continue
		}
		if info.Mode().Perm() != tc.wantMod {
			t.Errorf("%s mode = %v, want %v", dest, info.Mode().Perm(), tc.wantMod)
		}
	}
}

func TestMaterializeCreatesParentDirectories(t *testing.T) {
	target := t.TempDir()
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{
			"/home/dev/.config/deep/nested/file.txt": []byte("x"),
		},
	}

	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	nested := filepath.Join(target, ".config", "deep", "nested")
	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("expected parent directory %s to exist: %v", nested, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", nested)
	}
	// MkdirAll masks the mode with the process umask, so assert no bits outside
	// 0o750 and no world access rather than the exact value: a stricter umask is
	// still correct.
	if got := info.Mode().Perm(); got&^0o750 != 0 || got&0o007 != 0 {
		t.Errorf("%s mode = %o, want no wider than 0o750", nested, got)
	}
}

// TestMaterializeWritesSingleByteMergedConfig pins the merged write at the
// len(cf.Merged) > 0 boundary: a one-byte merged payload must still be written,
// so the guard is not satisfied by "more than one byte".
func TestMaterializeWritesSingleByteMergedConfig(t *testing.T) {
	target := t.TempDir()
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte("x"),
		MergedPath:  opencodeMergedPath,
	}

	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, ".config", "opencode", "opencode.jsonc"))
	if err != nil {
		t.Fatalf("read materialized merged config: %v", err)
	}
	if string(got) != "x" {
		t.Errorf("merged config = %q, want the one-byte payload", got)
	}
}

func TestMaterializeRejectsPathOutsideVMHome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		vmPath string
	}{
		{name: "sibling directory", vmPath: "/etc/passwd"},
		{name: "the home's parent", vmPath: "/home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cf := &ConfigFiles{
				HomeFiles: map[string][]byte{tc.vmPath: []byte("x")},
			}

			err := Materialize(cf, VMHomeDir, t.TempDir())
			if err == nil {
				t.Fatal("Materialize with a VM path outside the home = nil error, want an error")
			}
			if !strings.Contains(err.Error(), "not under") {
				t.Errorf("Materialize error = %v, want it to name the escaping path", err)
			}
		})
	}
}

// TestMaterializeRejectsRemovalPathOutsideVMHome pins that a Remove path that
// would escape the target is refused before any file is written, so a stale
// removal cannot reach outside the materialized tree.
func TestMaterializeRejectsRemovalPathOutsideVMHome(t *testing.T) {
	cf := &ConfigFiles{
		Remove: []string{"/etc/passwd"},
	}

	err := Materialize(cf, VMHomeDir, t.TempDir())
	if err == nil {
		t.Fatal("Materialize with a removal path outside the home = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "not under") {
		t.Errorf("Materialize error = %v, want it to name the escaping removal path", err)
	}
}

// TestMaterializeReportsMergedWriteFailure pins that a failure creating the
// merged config's parent directory is reported, wrapped so the caller can name
// the merged step.
func TestMaterializeReportsMergedWriteFailure(t *testing.T) {
	target := t.TempDir()
	// A regular file where the merged config's parent directory must go makes
	// MkdirAll fail with ENOTDIR.
	if err := os.WriteFile(filepath.Join(target, ".config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte("{}\n"),
		MergedPath:  opencodeMergedPath,
	}

	err := Materialize(cf, VMHomeDir, target)
	if err == nil {
		t.Fatal("Materialize over a blocked parent = nil error, want the mkdir failure")
	}
	if !strings.Contains(err.Error(), "materialize the agent's main config") {
		t.Errorf("Materialize error = %v, want it to name the merged step", err)
	}
	if !strings.Contains(err.Error(), "create parent directory") {
		t.Errorf("Materialize error = %v, want the mkdir failure named", err)
	}
	if _, ok := errors.AsType[*fs.PathError](err); !ok {
		t.Errorf("Materialize error = %v, want the mkdir cause wrapped", err)
	}
}

// TestMaterializeReportsFileWriteFailure pins that a group file that cannot be
// written is reported with the underlying cause wrapped, so a caller can match
// it. A destination that already exists as a directory makes WriteFile fail.
func TestMaterializeReportsFileWriteFailure(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ".config", "tool", "cfg.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v")},
	}

	err := Materialize(cf, VMHomeDir, target)
	if err == nil {
		t.Fatal("Materialize over a directory destination = nil error, want the write failure")
	}
	if !strings.Contains(err.Error(), "write ") {
		t.Errorf("Materialize error = %v, want the write failure", err)
	}
	if _, ok := errors.AsType[*fs.PathError](err); !ok {
		t.Errorf("Materialize error = %v, want the underlying path error wrapped", err)
	}
}

// TestMaterializeReportsChmodFailure pins that a permission change that fails is
// reported rather than mistaken for a successful write.
func TestMaterializeReportsChmodFailure(t *testing.T) {
	sentinel := errors.New("chmod boom")
	orig := chmodPath
	chmodPath = func(string, os.FileMode) error { return sentinel }
	t.Cleanup(func() { chmodPath = orig })

	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v")},
	}

	err := Materialize(cf, VMHomeDir, t.TempDir())
	if err == nil {
		t.Fatal("Materialize with a failing chmod = nil error, want the chmod failure")
	}
	if !strings.Contains(err.Error(), "chmod ") {
		t.Errorf("Materialize error = %v, want the chmod failure", err)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Materialize error = %v, want the chmod cause wrapped", err)
	}
}

// TestMaterializeReportsUnmappablePath pins that a VM path that cannot be made
// relative to the home (a relative home against an absolute path) is reported
// rather than silently mis-joined.
func TestMaterializeReportsUnmappablePath(t *testing.T) {
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v")},
	}

	err := Materialize(cf, "relative/home", t.TempDir())
	if err == nil {
		t.Fatal("Materialize with a relative home = nil error, want the mapping failure")
	}
	if !strings.Contains(err.Error(), "map VM path") {
		t.Errorf("Materialize error = %v, want it to name the unmappable path", err)
	}
}

// TestTargetPathWrapsTheRelFailure pins that a path filepath.Rel cannot map is
// reported with the underlying cause wrapped. filepath.Rel returns a plain
// errors.New with no exported identity and whose message %v would also embed, so
// the wrap is asserted by unwrap depth rather than by message.
func TestTargetPathWrapsTheRelFailure(t *testing.T) {
	_, err := targetPath("/home/dev/x", "relative/home", t.TempDir())
	if err == nil {
		t.Fatal("targetPath with a relative home = nil error, want the mapping failure")
	}
	if !strings.Contains(err.Error(), "map VM path") {
		t.Errorf("targetPath error = %v, want it to name the unmappable path", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("targetPath error = %v, want the filepath.Rel cause wrapped", err)
	}
}

func TestMaterializeRemovesStaleTargetsBeforeWriting(t *testing.T) {
	target := t.TempDir()
	stale := filepath.Join(target, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte("{}\n"),
		MergedPath:  opencodeMergedPath,
		Remove:      []string{"/home/dev/.config/opencode/opencode.json"},
	}

	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale target %s still present (stat err = %v), want it removed", stale, err)
	}
}

package reprovision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeWritesEveryFileGroup(t *testing.T) {
	target := t.TempDir()
	cf := &ConfigFiles{
		Merged:     []byte("{\n  \"model\": \"x\"\n}\n"),
		MergedPath: opencodeMergedPath,
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
}

func TestMaterializeRejectsPathOutsideVMHome(t *testing.T) {
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{"/etc/passwd": []byte("x")},
	}

	err := Materialize(cf, VMHomeDir, t.TempDir())
	if err == nil {
		t.Fatal("Materialize with a VM path outside the home = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "not under") {
		t.Errorf("Materialize error = %v, want it to name the escaping path", err)
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
		Merged:     []byte("{}\n"),
		MergedPath: opencodeMergedPath,
		Remove:     []string{"/home/dev/.config/opencode/opencode.json"},
	}

	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale target %s still present (stat err = %v), want it removed", stale, err)
	}
}

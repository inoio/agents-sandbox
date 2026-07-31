package testhelpers

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

func NewTestio(t *testing.T) stdio.Mock {
	t.Helper()
	return stdio.Mock{}
}

func WriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	WritePath(t, filepath.Join(dir, name), content)
}

func WritePath(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func WriteYAML(t *testing.T, dir, name string, v map[string]any) {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal yaml: %v", err)
	}
	WriteFile(t, dir, name, string(data))
}
func RunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func InitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	RunGit(t, dir, "init", "-b", "main")
	ConfigureRepo(t, dir)
	WriteFile(t, dir, "README.md", "hello")
	RunGit(t, dir, "add", "README.md")
	RunGit(t, dir, "commit", "-m", "initial")
	return dir
}

func ConfigureRepo(t *testing.T, dir string) {
	t.Helper()
	RunGit(t, dir, "config", "user.email", "test@example.com")
	RunGit(t, dir, "config", "user.name", "Test User")
}

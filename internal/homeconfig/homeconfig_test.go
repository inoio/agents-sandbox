package homeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

const vmHome = "/home/dev"

func writeHomeYAML(t *testing.T, dir, body string) {
	t.Helper()
	testutil.WriteFile(t, dir, "home.yaml", body)
}

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	writeHomeYAML(t, dir, ".gitconfig:\n.config/tool/cfg.toml: ./tool/cfg.toml\n")
	m, err := LoadManifest(filepath.Join(dir, "home.yaml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	want := map[string]string{
		".gitconfig":            "",
		".config/tool/cfg.toml": "./tool/cfg.toml",
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func TestMergeManifestsProjectWins(t *testing.T) {
	user := map[string]string{".gitconfig": "", ".ssh/config": ""}
	proj := map[string]string{".gitconfig": "~/dotfiles/gitconfig"}
	merged := MergeManifests(user, proj)
	if merged[".ssh/config"] != "" {
		t.Errorf("user-only key should remain, got %q", merged[".ssh/config"])
	}
	if merged[".gitconfig"] != "~/dotfiles/gitconfig" {
		t.Errorf("project should override user, got %q", merged[".gitconfig"])
	}
}

func TestResolveSourceEmptyFallsBackToTarget(t *testing.T) {
	got, err := ResolveSource(".gitconfig", "", "/tmp/mf")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got == "" {
		t.Fatal("expected a non-empty resolved source")
	}
	if !strings.HasSuffix(got, ".gitconfig") {
		t.Errorf("expected suffix .gitconfig, got %q", got)
	}
}

func TestResolveSourceAbsolute(t *testing.T) {
	got, err := ResolveSource(".x", "/etc/foo", "/tmp/mf")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got != "/etc/foo" {
		t.Errorf("expected absolute source, got %q", got)
	}
}

func TestResolveSourceTilde(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	got, err := ResolveSource(".x", "~/bar", "/tmp/mf")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got != "/home/u/bar" {
		t.Errorf("expected ~ expansion, got %q", got)
	}
}

func TestResolveSourceRelativeToManifestDir(t *testing.T) {
	got, err := ResolveSource(".x", "tool/cfg.toml", "/home/u/dotfiles")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got != "/home/u/dotfiles/tool/cfg.toml" {
		t.Errorf("expected manifest-dir-relative source, got %q", got)
	}
}

func TestResolveVMTargetValid(t *testing.T) {
	got, err := ResolveVMTarget(vmHome, ".config/tool/cfg.toml")
	if err != nil {
		t.Fatalf("ResolveVMTarget: %v", err)
	}
	if got != "/home/dev/.config/tool/cfg.toml" {
		t.Errorf("unexpected vm target, got %q", got)
	}
}

func TestResolveVMTargetRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"..", "../evil", "a/../../evil", "/etc/passwd", ""} {
		if _, err := ResolveVMTarget(vmHome, bad); err == nil {
			t.Errorf("expected error for target %q", bad)
		}
	}
}

func TestResolveVMTargetRejectsTilde(t *testing.T) {
	for _, bad := range []string{"~fdsa", "~/fdsa", "~/", "~"} {
		if _, err := ResolveVMTarget(vmHome, bad); err == nil {
			t.Errorf("expected error for target %q", bad)
		}
	}
}

func TestResolveVMTargetReservedOpencodeJSON(t *testing.T) {
	_, err := ResolveVMTarget(vmHome, ".config/opencode/opencode.json")
	if err == nil {
		t.Error("expected error for reserved opencode.json target")
	}
}

func TestResolveVMTargetRejectsReservedNonCanonicalSpellings(t *testing.T) {
	for _, bad := range []string{
		"./.config/opencode/opencode.json",
		".config/opencode//opencode.json",
		".config/opencode/./opencode.json",
	} {
		if _, err := ResolveVMTarget(vmHome, bad); err == nil {
			t.Errorf("expected error for reserved opencode.json spelled %q", bad)
		}
	}
}

func TestBuildHomeFilesSkipsMissingSource(t *testing.T) {
	// Point $HOME at an empty temp dir so the empty-source default
	// (host $HOME/.gitconfig) deterministically does not exist.
	t.Setenv("HOME", t.TempDir())
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, user, ".gitconfig:\n")
	files, _, err := BuildHomeFiles(user, proj, vmHome)
	if err != nil {
		t.Fatalf("BuildHomeFiles: %v", err)
	}
	if _, ok := files["/home/dev/.gitconfig"]; ok {
		t.Error("expected missing source to be skipped, but entry present")
	}
}

func TestDescribeManifestListsAllMappings(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, user, ".gitconfig:\n")
	writeHomeYAML(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")
	// cfg.toml need NOT exist for DescribeManifest.
	pairs, has, err := DescribeManifest(user, proj, vmHome)
	if err != nil {
		t.Fatalf("DescribeManifest: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	got := map[string]string{}
	for _, p := range pairs {
		got[p[0]] = p[1]
	}
	if _, ok := got["/home/dev/.gitconfig"]; !ok {
		t.Error("expected user .gitconfig mapping")
	}
	if _, ok := got["/home/dev/.config/tool/cfg.toml"]; !ok {
		t.Error("expected project cfg.toml mapping")
	}
}

func TestDescribeManifestSortsPairsByVMPath(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, proj, ""+
		".zshrc:\n"+
		".a:\n"+
		".M:\n"+
		".config/tool/cfg.toml: ./cfg.toml\n"+
		".b:\n")

	pairs, _, err := DescribeManifest(user, proj, vmHome)
	if err != nil {
		t.Fatalf("DescribeManifest: %v", err)
	}
	var paths []string
	for _, p := range pairs {
		paths = append(paths, p[0])
	}
	want := []string{
		"/home/dev/.M",
		"/home/dev/.a",
		"/home/dev/.b",
		"/home/dev/.config/tool/cfg.toml",
		"/home/dev/.zshrc",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("DescribeManifest pairs not sorted by VM path:\ngot  %v\nwant %v", paths, want)
	}
}

func TestDescribeManifestNoManifestHasFalse(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	pairs, has, err := DescribeManifest(user, proj, vmHome)
	if err != nil {
		t.Fatalf("DescribeManifest: %v", err)
	}
	if has {
		t.Error("expected has=false when no manifest exists")
	}
	if len(pairs) != 0 {
		t.Errorf("expected no pairs, got %d", len(pairs))
	}
}

func TestDescribeManifestEmptyManifestHasTrue(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, user, "")
	pairs, has, err := DescribeManifest(user, proj, vmHome)
	if err != nil {
		t.Fatalf("DescribeManifest: %v", err)
	}
	if !has {
		t.Error("expected has=true when an (empty) manifest exists")
	}
	if len(pairs) != 0 {
		t.Errorf("expected no pairs, got %d", len(pairs))
	}
}

func TestBuildHomeFilesReadsBytesByVMPath(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()

	// project manifest with an absolute source
	src := filepath.Join(proj, "cfg.toml")
	testutil.WritePath(t, src, "k=v\n")
	writeHomeYAML(t, proj, ".config/tool/cfg.toml: "+src+"\n")

	files, has, err := BuildHomeFiles(user, proj, vmHome)
	if err != nil {
		t.Fatalf("BuildHomeFiles: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := map[string][]byte{
		"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("got %v, want %v", files, want)
	}
}

func TestBuildHomeFilesReadsProjectRelativeSourceFromProjectDir(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()

	// project manifest with a source relative to the project manifest dir
	if err := os.MkdirAll(filepath.Join(proj, "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, proj, "tool/cfg.toml", "k=v\n")
	writeHomeYAML(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")

	files, has, err := BuildHomeFiles(user, proj, vmHome)
	if err != nil {
		t.Fatalf("BuildHomeFiles: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := map[string][]byte{
		"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("got %v, want %v", files, want)
	}
}

func TestDescribeManifestResolvesProjectRelativeSourceAgainstProjectDir(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")

	pairs, _, err := DescribeManifest(user, proj, vmHome)
	if err != nil {
		t.Fatalf("DescribeManifest: %v", err)
	}
	var src string
	for _, p := range pairs {
		if p[0] == "/home/dev/.config/tool/cfg.toml" {
			src = p[1]
		}
	}
	want := filepath.Join(proj, "tool/cfg.toml")
	if src != want {
		t.Errorf("got source %q, want %q", src, want)
	}
}

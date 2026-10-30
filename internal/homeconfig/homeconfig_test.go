package homeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

const vmHome = "/home/dev"

// testData wraps an inner home body under a top-level home: key and writes it
// as the user config file.
func writeHomeConfig(t *testing.T, dir, body string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("home:\n")
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sb.WriteString("  " + line + "\n")
	}
	testutil.WriteFile(t, dir, "config.yaml", sb.String())
}

// homeContents reduces the with-modes result to the byte map the retired
// BuildHomeFiles wrapper returned, so these tests keep asserting exactly the
// content they asserted before the wrapper was deleted.
func homeContents(files map[string]HomeFile) map[string][]byte {
	contents := make(map[string][]byte, len(files))
	for path, file := range files {
		contents[path] = file.Data
	}
	return contents
}

func TestParseHomeSection(t *testing.T) {
	data := []byte("home:\n  .gitconfig:\n  .config/tool/cfg.toml: ./tool/cfg.toml\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := Manifest{
		".gitconfig":            Entry{Source: ""},
		".config/tool/cfg.toml": Entry{Source: "./tool/cfg.toml"},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func TestParseHomeSectionNoHomeKey(t *testing.T) {
	data := []byte("cpus: 2\nmemory: 4G\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if has {
		t.Error("expected has=false when no home key")
	}
	if len(m) != 0 {
		t.Errorf("expected empty manifest, got %v", m)
	}
}

func TestParseHomeSectionEmptyHomeKey(t *testing.T) {
	data := []byte("home:\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if !has {
		t.Error("expected has=true for an empty home key")
	}
	if len(m) != 0 {
		t.Errorf("expected empty manifest, got %v", m)
	}
}

// TestDescribeLayersKeepsUserOnlyKeys covers the half of the retired
// MergeManifests wrapper's behaviour that TestDescribeLayersProjectWinsOnOverlap
// does not: a key only the user layer declares survives the merge.
func TestDescribeLayersKeepsUserOnlyKeys(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".ssh/config:\n")
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./tool.toml\n")

	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	for _, p := range pairs {
		if p[0] == "/home/dev/.ssh/config" {
			return
		}
	}
	t.Errorf("a key only the user layer declares should remain, got %v", pairs)
}

func TestParseHomeSectionStructuredEntry(t *testing.T) {
	data := []byte("home:\n  .vpn/connect.sh:\n    source: vpn/connect.sh\n    hook: startup\n    root: true\n")
	m, has, err := ParseHomeSection(data, "config.yaml")
	if err != nil {
		t.Fatalf("ParseHomeSection: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := Manifest{
		".vpn/connect.sh": Entry{Source: "vpn/connect.sh", Hook: "startup", Root: true},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func TestParseHomeSectionSyntaxErrorIsFriendly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("home:\n  .gitconfig:\n    source: [\n")
	_, _, err := ParseHomeSection(data, path)
	if err == nil {
		t.Fatal("expected error for malformed home section")
	}
	for _, want := range []string{path, "invalid YAML", "line"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestParseHomeSectionRejectsUnknownHook(t *testing.T) {
	data := []byte("home:\n  .x:\n    source: x\n    hook: boot\n")
	if _, _, err := ParseHomeSection(data, "config.yaml"); err == nil {
		t.Fatal("expected error for unknown hook value")
	}
}

func TestParseHomeSectionRejectsNonBooleanRoot(t *testing.T) {
	data := []byte("home:\n  .x:\n    source: x\n    hook: startup\n    root: yes\n")
	if _, _, err := ParseHomeSection(data, "config.yaml"); err == nil {
		t.Fatal("expected error for non-boolean root value")
	}
}

func TestResolveSourceEmptyFallsBackToTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveManifestSource(".gitconfig", "", "/tmp/mf")
	if want := filepath.Join(home, ".gitconfig"); got != want {
		t.Errorf("empty source = %q, want %q", got, want)
	}
}

func TestResolveSourceAbsolute(t *testing.T) {
	got := ResolveManifestSource(".x", "/etc/foo", "/tmp/mf")
	if got != "/etc/foo" {
		t.Errorf("expected absolute source, got %q", got)
	}
}

func TestResolveSourceTilde(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveManifestSource(".x", "~/bar", "/tmp/mf")
	if want := filepath.Join(home, "bar"); got != want {
		t.Errorf("~ source = %q, want %q", got, want)
	}
}

func TestResolveSourceRelativeToManifestDir(t *testing.T) {
	got := ResolveManifestSource(".x", "tool/cfg.toml", "/home/u/dotfiles")
	if got != "/home/u/dotfiles/tool/cfg.toml" {
		t.Errorf("expected manifest-dir-relative source, got %q", got)
	}
}

func TestResolveVMTargetValid(t *testing.T) {
	got, err := ResolveVMTarget(vmHome, ".config/tool/cfg.toml", nil)
	if err != nil {
		t.Fatalf("ResolveVMTarget: %v", err)
	}
	if got != "/home/dev/.config/tool/cfg.toml" {
		t.Errorf("unexpected vm target, got %q", got)
	}
}

func TestResolveVMTargetRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"..", "../evil", "a/../../evil", "/etc/passwd", ""} {
		if _, err := ResolveVMTarget(vmHome, bad, nil); err == nil {
			t.Errorf("expected error for target %q", bad)
		}
	}
}

func TestResolveVMTargetRejectsTilde(t *testing.T) {
	for _, bad := range []string{"~fdsa", "~/fdsa", "~/", "~"} {
		if _, err := ResolveVMTarget(vmHome, bad, nil); err == nil {
			t.Errorf("expected error for target %q", bad)
		}
	}
}

func TestResolveVMTargetReservedPath(t *testing.T) {
	_, err := ResolveVMTarget(vmHome, ".config/opencode/opencode.jsonc", []string{".config/opencode/opencode.jsonc"})
	if err == nil {
		t.Error("expected error for a target listed in reserved")
	}
}

func TestResolveVMTargetEmptyReservedAcceptsAnyPath(t *testing.T) {
	// A previously-reserved opencode path is accepted when reserved is empty,
	// so callers without a reserved merged-config path can provision it.
	got, err := ResolveVMTarget(vmHome, ".config/opencode/opencode.jsonc", nil)
	if err != nil {
		t.Fatalf("ResolveVMTarget with empty reserved: %v", err)
	}
	if got != "/home/dev/.config/opencode/opencode.jsonc" {
		t.Errorf("unexpected vm target, got %q", got)
	}
}

func TestResolveVMTargetRejectsReservedNonCanonicalSpellings(t *testing.T) {
	reserved := []string{".config/opencode/opencode.jsonc"}
	for _, bad := range []string{
		"./.config/opencode/opencode.jsonc",
		".config/opencode//opencode.jsonc",
		".config/opencode/./opencode.jsonc",
	} {
		if _, err := ResolveVMTarget(vmHome, bad, reserved); err == nil {
			t.Errorf("expected error for reserved opencode.json spelled %q", bad)
		}
	}
}

func TestResolveVMTargetAcceptsNonMatchingReservedSpelling(t *testing.T) {
	// The comparison is on the cleaned target, so a different path is accepted.
	got, err := ResolveVMTarget(vmHome, ".config/opencode/opencode.json", []string{".config/opencode/opencode.jsonc"})
	if err != nil {
		t.Fatalf("ResolveVMTarget: %v", err)
	}
	if got != "/home/dev/.config/opencode/opencode.json" {
		t.Errorf("unexpected vm target, got %q", got)
	}
}

func TestBuildHomeFilesSkipsMissingSource(t *testing.T) {
	// The mocked home is an empty temp dir, so the empty-source default
	// (host $HOME/.gitconfig) deterministically does not exist.
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n")
	files, missing, _, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if _, ok := files["/home/dev/.gitconfig"]; ok {
		t.Error("expected missing source to be skipped, but entry present")
	}
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing source, got %v", missing)
	}
	want := filepath.Join(home, ".gitconfig")
	if missing[0] != want {
		t.Errorf("missing source = %q, want %q", missing[0], want)
	}
}

func TestBuildHomeFilesRejectsReservedTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, proj, ".config/opencode/opencode.jsonc:\n")
	if _, _, _, err := BuildHomeFilesWithModes(
		user,
		proj,
		vmHome,
		[]string{".config/opencode/opencode.jsonc"},
	); err == nil {
		t.Error("expected an error for a reserved home target")
	}
}

func TestDescribeLayersRejectsReservedTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, proj, ".pi/agent/settings.json:\n")
	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if _, err := DescribeLayers(layers, vmHome, []string{".pi/agent/settings.json"}); err == nil {
		t.Error("expected an error for a reserved home target")
	}
}

func TestLoadLayersPerDirResolution(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".gitconfig:\n")
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")

	layers, has, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if len(layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(layers))
	}
	if layers[0].Dir != user || layers[1].Dir != proj {
		t.Errorf("layer dirs = %q, %q; want %q, %q", layers[0].Dir, layers[1].Dir, user, proj)
	}
}

func TestDescribeLayersResolvesProjectRelativeAgainstProjectDir(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")

	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	want := filepath.Join(proj, "tool/cfg.toml")
	var src string
	for _, p := range pairs {
		if p[0] == "/home/dev/.config/tool/cfg.toml" {
			src = p[1]
		}
	}
	if src != want {
		t.Errorf("got source %q, want %q", src, want)
	}
}

func TestDescribeLayersSortsPairsByVMPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, proj, ""+
		".zshrc:\n"+
		".a:\n"+
		".M:\n"+
		".config/tool/cfg.toml: ./cfg.toml\n"+
		".b:\n")

	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
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
		t.Errorf("DescribeLayers pairs not sorted by VM path:\ngot  %v\nwant %v", paths, want)
	}
}

func TestLoadLayersNoManifestHasFalse(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	layers, has, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if has {
		t.Error("expected has=false when no manifest exists")
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no pairs, got %d", len(pairs))
	}
}

func TestLoadLayersEmptyManifestHasTrue(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, "")
	layers, has, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if !has {
		t.Error("expected has=true when an (empty) manifest exists")
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
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
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: "+src+"\n")

	files, _, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := map[string][]byte{
		"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
	}
	if got := homeContents(files); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildHomeFilesWithModesPreservesSourceMode(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	source := filepath.Join(proj, "launcher.sh")
	testutil.WritePath(t, source, "#!/bin/sh\n")
	if err := os.Chmod(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHomeConfig(t, proj, ".config/tool/launcher.sh: ./launcher.sh\n")

	files, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has || len(missing) != 0 {
		t.Fatalf("has = %v, missing = %v; want manifest without missing sources", has, missing)
	}
	file, ok := files["/home/dev/.config/tool/launcher.sh"]
	if !ok {
		t.Fatalf("home file missing: %v", files)
	}
	if string(file.Data) != "#!/bin/sh\n" {
		t.Errorf("file content = %q, want shebang", file.Data)
	}
	if file.Mode.Perm() != 0o755 {
		t.Errorf("file mode = %o, want 755", file.Mode.Perm())
	}
}

// TestBuildHomeFilesWithModesReportsUnreadableSourceAsMissing verifies a source
// that exists but cannot be read as a file (here, a directory) is treated as a
// missing source rather than a fatal error, so a stale home entry never blocks
// VM setup.
func TestBuildHomeFilesWithModesReportsUnreadableSourceAsMissing(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	sourceDir := filepath.Join(proj, "not-a-file")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHomeConfig(t, proj, ".config/tool/x: "+sourceDir+"\n")

	files, missing, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if len(files) != 0 {
		t.Errorf("unreadable source must yield no home file, got %v", files)
	}
	if len(missing) != 1 || missing[0] != sourceDir {
		t.Errorf("missing = %v, want [%q]", missing, sourceDir)
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
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")

	files, _, has, err := BuildHomeFilesWithModes(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFilesWithModes: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	want := map[string][]byte{
		"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
	}
	if got := homeContents(files); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildHooksFiltersAndSorts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	user := t.TempDir()
	proj := t.TempDir()

	// Only .vpn/connect.sh is marked hook: startup and its source exists.
	if err := os.MkdirAll(filepath.Join(proj, "vpn"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(proj, "vpn/connect.sh"), "#!/bin/sh\necho hi\n")
	writeHomeConfig(t, proj, ""+
		".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n  root: true\n"+
		".zshrc:\n")

	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	want := []HookSpec{
		{
			Target:     "/home/dev/.vpn/connect.sh",
			Source:     filepath.Join(proj, "vpn/connect.sh"),
			HasShebang: true,
			Root:       true,
		},
	}
	if !reflect.DeepEqual(hooks, want) {
		t.Errorf("got %v, want %v", hooks, want)
	}
}

func TestBuildHooksSkipsMissingSource(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	// hook entry whose source file does not exist on the host
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 0 {
		t.Errorf("expected no hooks for missing source, got %v", hooks)
	}
}

func TestHasShebang(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"plain sh", "#!/bin/sh\n", true},
		{"bash", "#!/bin/bash\necho hi\n", true},
		{"env bash", "#!/usr/bin/env bash\n", true},
		{"with spaces", "#!/usr/bin/env python3\n", true},
		{"with arg", "#!/bin/bash -e\n", true},
		{"no shebang", "echo hi\n", false},
		{"empty file", "", false},
		{"bare marker", "#! \n", true},
		{"crlf shebang", "#!/bin/bash\r\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, "s.sh")
			testutil.WritePath(t, p, tc.body)
			if got := hasShebang(p); got != tc.want {
				t.Errorf("hasShebang(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestBuildHooksProjectWinsOnOverlap(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "vpn"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(user, "vpn"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(proj, "vpn/connect.sh"), "#!/bin/sh\necho proj\n")
	testutil.WritePath(t, filepath.Join(user, "vpn/connect.sh"), "#!/bin/sh\necho user\n")
	writeHomeConfig(t, user, ".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n  root: true\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n  root: true\n")

	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("expected exactly 1 hook (project wins), got %d: %v", len(hooks), hooks)
	}
	want := filepath.Join(proj, "vpn/connect.sh")
	if hooks[0].Source != want {
		t.Errorf("expected project source %q, got %q", want, hooks[0].Source)
	}
}

func TestDescribeLayersProjectWinsOnOverlap(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeConfig(t, user, ".config/tool/cfg.toml: ./user.toml\n")
	writeHomeConfig(t, proj, ".config/tool/cfg.toml: ./proj.toml\n")

	layers, _, err := LoadLayers([]string{user, proj})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	pairs, err := DescribeLayers(layers, vmHome, nil)
	if err != nil {
		t.Fatalf("DescribeLayers: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected exactly 1 pair (project wins), got %d: %v", len(pairs), pairs)
	}
	want := filepath.Join(proj, "proj.toml")
	if pairs[0][1] != want {
		t.Errorf("expected project source %q, got %q", want, pairs[0][1])
	}
}

func TestBuildHooksCapturesShebang(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "connect.sh"), "#!/bin/bash\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 || !hooks[0].HasShebang {
		t.Errorf("hooks = %v, want a hook with a shebang", hooks)
	}
}

func TestBuildHooksCapturesEnvShebang(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "connect.sh"), "#!/usr/bin/env bash\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks = %v, want 1 hook", hooks)
	}
	if !hooks[0].HasShebang {
		t.Errorf("hooks[0].HasShebang = %v, want true", hooks[0].HasShebang)
	}
}

func TestBuildHooksNoShebangFallsBack(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "connect.sh"), "echo hi\n")
	writeHomeConfig(t, proj, ".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks = %v, want 1 hook", hooks)
	}
	if hooks[0].HasShebang {
		t.Errorf("hooks[0].HasShebang = %v, want false", hooks[0].HasShebang)
	}
}

func TestBuildHooksSortsByTarget(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "a.sh"), "#!/bin/sh\n")
	testutil.WritePath(t, filepath.Join(proj, "b.sh"), "#!/bin/sh\n")
	writeHomeConfig(t, proj, ""+
		".b:\n  source: b.sh\n  hook: startup\n"+
		".a:\n  source: a.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 2 || hooks[0].Target != "/home/dev/.a" || hooks[1].Target != "/home/dev/.b" {
		t.Errorf("hooks not sorted by target: %v", hooks)
	}
}

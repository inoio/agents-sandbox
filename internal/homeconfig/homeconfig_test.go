package homeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/testutil"
)

const vmHome = "/home/dev"

func writeHomeYAML(t *testing.T, dir, body string) {
	t.Helper()
	testutil.WriteFile(t, dir, "home.yaml", body)
}

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

func TestMergeManifestsProjectWins(t *testing.T) {
	user := Manifest{".gitconfig": Entry{Source: ""}, ".ssh/config": Entry{Source: ""}}
	proj := Manifest{".gitconfig": Entry{Source: "~/dotfiles/gitconfig"}}
	merged := MergeManifests(user, proj)
	if merged[".ssh/config"].Source != "" {
		t.Errorf("user-only key should remain, got %q", merged[".ssh/config"].Source)
	}
	if merged[".gitconfig"].Source != "~/dotfiles/gitconfig" {
		t.Errorf("project should override user, got %q", merged[".gitconfig"].Source)
	}
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
	got, err := ResolveManifestSource(".gitconfig", "", "/tmp/mf")
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
	got, err := ResolveManifestSource(".x", "/etc/foo", "/tmp/mf")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got != "/etc/foo" {
		t.Errorf("expected absolute source, got %q", got)
	}
}

func TestResolveSourceTilde(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	got, err := ResolveManifestSource(".x", "~/bar", "/tmp/mf")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got != "/home/u/bar" {
		t.Errorf("expected ~ expansion, got %q", got)
	}
}

func TestResolveSourceRelativeToManifestDir(t *testing.T) {
	got, err := ResolveManifestSource(".x", "tool/cfg.toml", "/home/u/dotfiles")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
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
	// Point $HOME at an empty temp dir so the empty-source default
	// (host $HOME/.gitconfig) deterministically does not exist.
	t.Setenv("HOME", t.TempDir())
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, user, ".gitconfig:\n")
	files, missing, _, err := BuildHomeFiles(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHomeFiles: %v", err)
	}
	if _, ok := files["/home/dev/.gitconfig"]; ok {
		t.Error("expected missing source to be skipped, but entry present")
	}
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing source, got %v", missing)
	}
	want := filepath.Join(os.Getenv("HOME"), ".gitconfig")
	if missing[0] != want {
		t.Errorf("missing source = %q, want %q", missing[0], want)
	}
}

func TestBuildHomeFilesRejectsReservedTarget(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, proj, ".config/opencode/opencode.jsonc:\n")
	if _, _, _, err := BuildHomeFiles(user, proj, vmHome, []string{".config/opencode/opencode.jsonc"}); err == nil {
		t.Error("expected an error for a reserved home target")
	}
}

func TestDescribeManifestRejectsReservedTarget(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, proj, ".pi/agent/settings.json:\n")
	if _, _, err := DescribeManifest(user, proj, vmHome, []string{".pi/agent/settings.json"}); err == nil {
		t.Error("expected an error for a reserved home target")
	}
}

func TestDescribeManifestListsAllMappings(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeHomeYAML(t, user, ".gitconfig:\n")
	writeHomeYAML(t, proj, ".config/tool/cfg.toml: ./tool/cfg.toml\n")
	// cfg.toml need NOT exist for DescribeManifest.
	pairs, has, err := DescribeManifest(user, proj, vmHome, nil)
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

	pairs, _, err := DescribeManifest(user, proj, vmHome, nil)
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
	pairs, has, err := DescribeManifest(user, proj, vmHome, nil)
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
	pairs, has, err := DescribeManifest(user, proj, vmHome, nil)
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

	files, _, has, err := BuildHomeFiles(user, proj, vmHome, nil)
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

	files, _, has, err := BuildHomeFiles(user, proj, vmHome, nil)
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

	pairs, _, err := DescribeManifest(user, proj, vmHome, nil)
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

func TestBuildHooksFiltersAndSorts(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()

	// Only .vpn/connect.sh is marked hook: startup and its source exists.
	if err := os.MkdirAll(filepath.Join(proj, "vpn"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(proj, "vpn/connect.sh"), "#!/bin/sh\necho hi\n")
	writeHomeYAML(t, proj, ""+
		".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n  root: true\n"+
		".zshrc:\n")

	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	want := []HookSpec{
		{
			Target:      "/home/dev/.vpn/connect.sh",
			Source:      filepath.Join(proj, "vpn/connect.sh"),
			Interpreter: "/bin/sh",
			Root:        true,
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
	writeHomeYAML(t, proj, ".vpn/connect.sh:\n  source: vpn/connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 0 {
		t.Errorf("expected no hooks for missing source, got %v", hooks)
	}
}

func TestShebangInterpreter(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"plain sh", "#!/bin/sh\n", "/bin/sh"},
		{"bash", "#!/bin/bash\necho hi\n", "/bin/bash"},
		{"env bash", "#!/usr/bin/env bash\n", "/usr/bin/env bash"},
		{"with spaces", "#!/usr/bin/env python3\n", "/usr/bin/env python3"},
		{"no shebang", "echo hi\n", ""},
		{"empty file", "", ""},
		{"crlf shebang", "#!/bin/bash\r\n", "/bin/bash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, "s.sh")
			testutil.WritePath(t, p, tc.body)
			if got := shebangInterpreter(p); got != tc.want {
				t.Errorf("shebangInterpreter(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestBuildHooksCapturesInterpreter(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "connect.sh"), "#!/bin/bash\n")
	writeHomeYAML(t, proj, ".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n")
	hooks, err := BuildHooks(user, proj, vmHome, nil)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].Interpreter != "/bin/bash" {
		t.Errorf("hooks = %v, want interpreter /bin/bash", hooks)
	}
}

func TestBuildHooksSortsByTarget(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	testutil.WritePath(t, filepath.Join(proj, "a.sh"), "#!/bin/sh\n")
	testutil.WritePath(t, filepath.Join(proj, "b.sh"), "#!/bin/sh\n")
	writeHomeYAML(t, proj, ""+
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

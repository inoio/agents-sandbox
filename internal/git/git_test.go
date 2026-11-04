package git

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestSanitizeFolderNameBoundaries pins the separator predicate at each ASCII
// edge: the digit range, the gap between '9' and 'a', and the top of the
// lowercase range. A char just outside a case must be treated as a separator;
// the boundary char itself must not be.
func TestSanitizeFolderNameBoundaries(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a0b", "a0b"},
		{"a9b", "a9b"},
		{"aZb", "azb"},
		{"a:b", "a-b"},
		{"a{b", "a-b"},
		{"a?b", "a-b"},
	}
	for _, tc := range cases {
		if got := sanitizeFolderName(tc.in); got != tc.want {
			t.Errorf("sanitizeFolderName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeFolderNameLowercases(t *testing.T) {
	got := sanitizeFolderName("MyApp")
	if got != "myapp" {
		t.Errorf("expected %q, got %q", "myapp", got)
	}
}

func TestSanitizeFolderNameReplacesNonAlnum(t *testing.T) {
	got := sanitizeFolderName("My App")
	if got != "my-app" {
		t.Errorf("expected %q, got %q", "my-app", got)
	}
}

func TestSanitizeFolderNameCollapsesDashes(t *testing.T) {
	got := sanitizeFolderName("My---App!!!")
	if got != "my-app" {
		t.Errorf("expected %q, got %q", "my-app", got)
	}
}

func TestSanitizeFolderNameTrimsLeadingTrailingDashes(t *testing.T) {
	got := sanitizeFolderName("---leading-and-trailing---")
	if got != "leading-and-trailing" {
		t.Errorf("expected %q, got %q", "leading-and-trailing", got)
	}
}

func TestSanitizeFolderNameCapsAt20(t *testing.T) {
	got := sanitizeFolderName("abcdefghijklmnopqrstuvwxyz")
	if len(got) > 20 {
		t.Errorf("expected <= 20 chars, got %d (%q)", len(got), got)
	}
}

func TestSanitizeFolderNameEmptyInput(t *testing.T) {
	got := sanitizeFolderName("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestHashIDReturns14Chars(t *testing.T) {
	got := HashID("test-input")
	if len(got) != 14 {
		t.Errorf("expected 14 chars, got %d (%q)", len(got), got)
	}
}

func TestHashIDDeterministic(t *testing.T) {
	a := HashID("sha256:abc123def456")
	b := HashID("sha256:abc123def456")
	if a != b {
		t.Errorf("expected deterministic output, got %q and %q", a, b)
	}
}

func TestHashIDDifferentInputs(t *testing.T) {
	a := HashID("hello")
	b := HashID("world")
	if a == b {
		t.Errorf("expected different hashes for different inputs, got %q for both", a)
	}
}

func TestHashIDBase36AlphabetOnly(t *testing.T) {
	got := HashID("sha256:fce5c4a3b2d1e0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5")
	for _, r := range got {
		isBase36 := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')
		if !isBase36 {
			t.Errorf("expected base36 alphabet only, found %q in %q", r, got)
		}
	}
}

func TestHashIDKnownValues(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "5oaq0bjhj6un82"},
		{"sha256:abc123def456", "3k5q07ywpibwp5"},
		{"hello", "14bu24ea7cq4jh"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := HashID(tt.input); got != tt.want {
				t.Errorf("HashID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestOriginURLEmptyableOriginRemote pins that an origin remote with no URL is
// reported as no origin, not indexed blindly.
func TestOriginURLEmptyableOriginRemote(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfg := "[core]\n\trepositoryformatversion = 0\n" +
		"[remote \"origin\"]\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := originURL(dir); got != "" {
		t.Errorf("originURL(%q) = %q, want empty", dir, got)
	}
}

// TestOriginURLNonRepoIgnoresCwdConfig pins that a directory outside a repo
// resolves to no origin even when the process CWD happens to hold a "config"
// file: the empty git dir short-circuits before any relative read.
func TestOriginURLNonRepoIgnoresCwdConfig(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "config"),
		[]byte("[remote \"origin\"]\n\turl = git@github.com:oops/should-not-be-read.git\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Chdir(cwd)
	if got := originURL(filepath.Join(cwd, "not-a-repo")); got != "" {
		t.Errorf("originURL() = %q, want empty", got)
	}
}

func TestOriginURL(t *testing.T) {
	t.Run("not a repository", func(t *testing.T) {
		dir := t.TempDir()
		if got := originURL(dir); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("returns origin url", func(t *testing.T) {
		const origin = "git@github.com:inoio/agents-sandbox.git"
		repo := testutil.InitRepo(t)
		testutil.RunGit(t, repo, "remote", "add", "origin", origin)
		if got := originURL(repo); got != origin {
			t.Errorf("originURL(%q) = %q, want %q", repo, got, origin)
		}
	})

	t.Run("no origin remote", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		if got := originURL(repo); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", repo, got)
		}
	})

	t.Run("missing config file", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		if err := os.Remove(filepath.Join(repo, ".git", "config")); err != nil {
			t.Fatalf("Remove config: %v", err)
		}
		if got := originURL(repo); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", repo, got)
		}
	})

	t.Run("malformed config file", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		if err := os.WriteFile(
			filepath.Join(repo, ".git", "config"),
			[]byte("not [valid] git config"),
			0o644,
		); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := originURL(repo); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", repo, got)
		}
	})
}

func TestReadRepoConfigMissingIsNotExist(t *testing.T) {
	_, err := readRepoConfig(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readRepoConfig() error = %v, want fs.ErrNotExist", err)
	}
}

func TestIsBare(t *testing.T) {
	repo := testutil.InitRepo(t)
	if isBare(filepath.Join(repo, ".git")) {
		t.Error("a normal checkout's git dir must not be bare")
	}
	if isBare(t.TempDir()) {
		t.Error("a git dir without a readable config must not be reported bare")
	}
}

// TestIsBareReadsWorktreeConfig pins that a bare repo is still detected when
// git has moved core.bare out of the shared config into the main worktree's
// config.worktree, which it does when extensions.worktreeConfig is set (for
// example once sparse-checkout is used).
func TestIsBareReadsWorktreeConfig(t *testing.T) {
	gitDir := writeWorktreeConfigFixture(t, worktreeConfigOn, "[core]\n\tbare = true\n")
	if !isBare(gitDir) {
		t.Errorf("isBare(%q) = false, want true when core.bare is in config.worktree", gitDir)
	}
}

// TestIsBareUnreadableWorktreeConfigIsNotBare pins that a config.worktree
// that cannot be read does not claim the repository is bare.
func TestIsBareUnreadableWorktreeConfigIsNotBare(t *testing.T) {
	gitDir := writeWorktreeConfigFixture(t, worktreeConfigOn, "")
	if err := os.Mkdir(filepath.Join(gitDir, "config.worktree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isBare(gitDir) {
		t.Errorf("isBare(%q) = true, want false for an unreadable config.worktree", gitDir)
	}
}

// TestIsBareIgnoresWorktreeConfigWithoutExtension pins that a config.worktree is
// not consulted unless extensions.worktreeConfig is enabled: git itself ignores
// the file then, so a leftover with core.bare must not make isBare report bare.
func TestIsBareIgnoresWorktreeConfigWithoutExtension(t *testing.T) {
	gitDir := writeWorktreeConfigFixture(t, "[core]\n\trepositoryformatversion = 1\n", "[core]\n\tbare = true\n")
	if isBare(gitDir) {
		t.Errorf("isBare(%q) = true, want false when extensions.worktreeConfig is disabled", gitDir)
	}
}

// TestIsBareWorktreeConfigEnabledStillNeedsCoreBare pins that enabling the
// extension only makes git read config.worktree; the file must itself set
// core.bare. An extension-enabled repo whose config.worktree does not claim
// bare is not bare.
func TestIsBareWorktreeConfigEnabledStillNeedsCoreBare(t *testing.T) {
	gitDir := writeWorktreeConfigFixture(t, worktreeConfigOn, "[core]\n\tbare = false\n")
	if isBare(gitDir) {
		t.Errorf("isBare(%q) = true, want false when config.worktree does not set core.bare", gitDir)
	}
}

// TestIsBareWorktreeConfigBooleanSpellings pins that enabling
// extensions.worktreeConfig is recognized for every boolean git accepts, not
// just "true": with the extension on and core.bare in config.worktree, each
// true spelling reports the repo bare and each false spelling does not.
func TestIsBareWorktreeConfigBooleanSpellings(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"True", true},
		{"yes", true},
		{"YES", true},
		{"on", true},
		{"1", true},
		{"2", true},
		{"-1", true},
		{"false", false},
		{"no", false},
		{"off", false},
		{"0", false},
		{"", false},
		{"nonsense", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			shared := "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tworktreeConfig = " + tc.value + "\n"
			gitDir := writeWorktreeConfigFixture(t, shared, "[core]\n\tbare = true\n")
			if got := isBare(gitDir); got != tc.want {
				t.Errorf("isBare with extensions.worktreeConfig = %q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestResolveGitDir(t *testing.T) {
	t.Run("missing dotgit", func(t *testing.T) {
		dir := t.TempDir()
		if got := resolveGitDir(dir); got != "" {
			t.Errorf("resolveGitDir(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("dotgit is a directory", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		want := filepath.Join(repo, ".git")
		if got := resolveGitDir(repo); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", repo, got, want)
		}
	})

	t.Run("linked worktree strips worktrees segment", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		wtDir := filepath.Join(t.TempDir(), "wt")
		testutil.RunGit(t, repo, "worktree", "add", "--detach", wtDir)
		want := filepath.Join(repo, ".git")
		if got := resolveGitDir(wtDir); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", wtDir, got, want)
		}
	})

	t.Run("relative worktree gitdir resolves against root", func(t *testing.T) {
		repo := t.TempDir()
		wtDir := filepath.Join(t.TempDir(), "wt")
		if err := os.MkdirAll(wtDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		// A registration needs its commondir marker, so a stripped-and-joined
		// relative gitdir is validated as genuine.
		candidate := filepath.Join(repo, ".git", "worktrees", "wt")
		if err := os.MkdirAll(candidate, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(candidate, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		relative, err := filepath.Rel(wtDir, candidate)
		if err != nil {
			t.Fatalf("Rel: %v", err)
		}
		if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+relative+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		want := filepath.Join(repo, ".git")
		if got := resolveGitDir(wtDir); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", wtDir, got, want)
		}
	})

	t.Run("dotgit file without worktrees segment", func(t *testing.T) {
		dir := t.TempDir()
		want := "/shared/.git"
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+want+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := resolveGitDir(dir); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", dir, got, want)
		}
	})

	t.Run("dotgit file without gitdir prefix", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("not-a-gitdir"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := resolveGitDir(dir); got != "" {
			t.Errorf("resolveGitDir(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("unreadable dotgit file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /x"), 0o000); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := os.ReadFile(filepath.Join(dir, ".git")); err == nil {
			t.Skip("file permissions are not enforced in this environment")
		}
		if got := resolveGitDir(dir); got != "" {
			t.Errorf("resolveGitDir(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("submodule gitdir under a worktrees-named ancestor", func(t *testing.T) {
		sub := newSubmoduleUnderWorktreesAncestor(t)
		want := filepath.Join(filepath.Dir(sub), ".git", "modules", "sub")
		if got := resolveGitDir(sub); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", sub, got, want)
		}
		if isLinkedWorktree(sub) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", sub)
		}
	})

	t.Run("relative gitdir under a worktrees-named ancestor", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "worktrees", "proj", "sub")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../shared.git\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		want := filepath.Join(root, "..", "shared.git")
		if got := resolveGitDir(root); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", root, got, want)
		}
		if isLinkedWorktree(root) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", root)
		}
	})

	t.Run("absolute gitdir under a worktrees-named ancestor", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "proj")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		shared := filepath.Join(t.TempDir(), "worktrees", "proj", "shared.git")
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+shared+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := resolveGitDir(root); got != shared {
			t.Errorf("resolveGitDir(%q) = %q, want %q", root, got, shared)
		}
		if isLinkedWorktree(root) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", root)
		}
	})

	t.Run("worktree registration under a worktrees-named ancestor", func(t *testing.T) {
		repo := newRepoUnderWorktreesAncestor(t)
		wt := filepath.Join(repo, "wt")
		testutil.RunGit(t, repo, "worktree", "add", wt)
		want := filepath.Join(repo, ".git")
		if got := resolveGitDir(wt); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", wt, got, want)
		}
		if !isLinkedWorktree(wt) {
			t.Errorf("isLinkedWorktree(%q) = false, want true", wt)
		}
	})
}

func TestIsLinkedWorktree(t *testing.T) {
	t.Run("dotgit directory", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		if isLinkedWorktree(repo) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", repo)
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		wtDir := filepath.Join(t.TempDir(), "wt")
		testutil.RunGit(t, repo, "worktree", "add", "--detach", wtDir)
		if !isLinkedWorktree(wtDir) {
			t.Errorf("isLinkedWorktree(%q) = false, want true", wtDir)
		}
	})

	t.Run("separate git dir", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /shared/repo.git\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if isLinkedWorktree(dir) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", dir)
		}
	})

	t.Run("missing dotgit", func(t *testing.T) {
		dir := t.TempDir()
		if isLinkedWorktree(dir) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", dir)
		}
	})
}

// TestWorktreeRegistrationRejectsMalformedNames pins the two name guards: an
// empty final segment (a trailing "/worktrees/") and a name containing "/" are
// rejected even when the candidate carries a commondir marker, so a malformed
// gitdir cannot strip to a shared dir.
func TestWorktreeRegistrationRejectsMalformedNames(t *testing.T) {
	base := mustResolvePath(t, t.TempDir())
	marker := func(dir string) string {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", dir, err)
		}
		return dir
	}
	emptyName := marker(filepath.Join(base, "one", "worktrees")) + string(filepath.Separator)
	if shared, ok := worktreeRegistration(base, emptyName); ok || shared != "" {
		t.Errorf("empty worktree name: got (%q, %v), want (\"\", false)", shared, ok)
	}
	slashedName := marker(filepath.Join(base, "two", "worktrees", "name", "sub"))
	if shared, ok := worktreeRegistration(base, slashedName); ok || shared != "" {
		t.Errorf("slashed worktree name: got (%q, %v), want (\"\", false)", shared, ok)
	}
}

// TestLastPathSegmentSeparators pins the successive strips: '@' userinfo, the
// last '/', the last ':', and the .git suffix, including a leading separator
// (index 0) and an '@' inside the final segment.
func TestLastPathSegmentSeparators(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"https userinfo and port", "https://user@host:8080/org/repo.git", "repo"},
		{"scheme no path", "https://host", "host"},
		{"at in final segment", "host/repo@v2.git", "v2"},
		{"leading at", "@host", "host"},
		{"leading slash", "/repo", "repo"},
		{"leading colon", ":repo", "repo"},
		{"no separator", "repo.git", "repo"},
	}
	for _, tc := range cases {
		if got := lastPathSegment(tc.url); got != tc.want {
			t.Errorf("lastPathSegment(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestLastPathSegment(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"https with .git", "https://gitlab.example.com/org/repo.git", "repo"},
		{"https no .git", "https://gitlab.example.com/org/my-repo", "my-repo"},
		{"ssh scp-like with namespace", "git@github.com:inoio/agents-sandbox.git", "agents-sandbox"},
		{"ssh scp-like no namespace", "git@gitlab.example.com:tool.git", "tool"},
		{"git protocol", "git://gitlab.example.com/org/repo.git", "repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastPathSegment(tt.url); got != tt.want {
				t.Errorf("lastPathSegment(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestProjectSlugFallsBackWhenFolderNameEmpty(t *testing.T) {
	got := projectSlug("", "some-id")
	if !strings.HasPrefix(got, "project-") {
		t.Errorf("expected slug to start with 'project-', got %q", got)
	}
	if len(got) != len("project-")+14 {
		t.Errorf("expected slug of %d chars, got %d (%q)", len("project-")+14, len(got), got)
	}
}

func TestProjectSlugUsesSanitizedFolderName(t *testing.T) {
	got := projectSlug("my-app", "some-id")
	if !strings.HasPrefix(got, "my-app-") {
		t.Errorf("expected slug to start with 'my-app-', got %q", got)
	}
	if len(got) != len("my-app-")+14 {
		t.Errorf("expected slug of %d chars, got %d (%q)", len("my-app-")+14, len(got), got)
	}
}

// writeWorktreeConfigFixture lays out a git directory whose shared config and,
// when worktreeConfig is non-empty, config.worktree carry the given contents,
// and returns the git dir path.
func writeWorktreeConfigFixture(t *testing.T, sharedConfig, worktreeConfig string) string {
	t.Helper()
	gitDir := filepath.Join(mustResolvePath(t, t.TempDir()), ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", gitDir, err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(sharedConfig), 0o644); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	if worktreeConfig != "" {
		if err := os.WriteFile(filepath.Join(gitDir, "config.worktree"), []byte(worktreeConfig), 0o644); err != nil {
			t.Fatalf("WriteFile(config.worktree): %v", err)
		}
	}
	return gitDir
}

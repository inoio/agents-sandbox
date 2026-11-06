//deadcode:ignore reason="test support: referenced only by _test.go files"
package testutil

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	formatcfg "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// gitIsolationEnvironment returns environ with the developer's git and ssh
// configuration neutralized, so a test that shells out to git can neither read
// nor prompt through the host's global or system config — commit signing
// (which pops a GUI askpass such as ksshaskpass), a credential helper, or an
// askpass program. The fixtures pin any git setting they depend on themselves.
func gitIsolationEnvironment(environ []string) []string {
	var isolated []string
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if isAmbientGitSetting(name) {
			continue
		}
		isolated = append(isolated, entry)
	}
	return append(isolated,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
	)
}

// isAmbientGitSetting reports whether name carries the developer's git or ssh
// configuration into a fixture: the global and system config files, the askpass
// programs, or git's per-invocation config channels (GIT_CONFIG_PARAMETERS and
// the GIT_CONFIG_COUNT/GIT_CONFIG_KEY_n/GIT_CONFIG_VALUE_n triplet), which stay
// active even when the config files are disabled — a poisoned commit.gpgsign
// would still sign and prompt.
func isAmbientGitSetting(name string) bool {
	switch name {
	case "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
		"GIT_ASKPASS", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE":
		return true
	}
	return strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

// RunGit executes git with the given args in dir and returns combined output.
func RunGit(tb testing.TB, dir string, args ...string) string {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = gitIsolationEnvironment(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// ConfigureRepo sets up basic git config in dir.
func ConfigureRepo(tb testing.TB, dir string) {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	RunGit(tb, dir, "config", "user.email", "test@example.com")
	RunGit(tb, dir, "config", "user.name", "Test User")
}

// InitRepo creates a new git repo in a temp dir with initial config and commit.
// It initializes the repo with go-git (in-process) rather than the git CLI:
// the resulting objects and config are standard, so real-git operations such as
// `git worktree add` work against it, but tests no longer pay for several `git`
// subprocess spawns on every call.
func InitRepo(tb testing.TB) string {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	dir := tb.TempDir()

	// keyed by field so exhaustruct sees every PlainInitOptions field; the
	// embedded InitOptions key is what exhaustruct needs but modernize's
	// embedlit rule dislikes, so suppress only that.
	//nolint:modernize // embedded InitOptions cannot be set without the key
	initOptions := git.PlainInitOptions{
		InitOptions:  git.InitOptions{DefaultBranch: plumbing.Main},
		Bare:         false,
		ObjectFormat: formatcfg.ObjectFormat(""),
	}
	repo, err := git.PlainInitWithOptions(dir, &initOptions)
	if err != nil {
		tb.Fatalf("init repo in %s: %v", dir, err)
	}

	cfg, err := repo.Config()
	// gomutants:disable-next-line BRANCH_IF reason="mid-init failure needs a production seam"
	if err != nil {
		tb.Fatalf("read repo config in %s: %v", dir, err)
	}
	cfg.User.Name = "Test User"
	cfg.User.Email = "test@example.com"
	// gomutants:disable-next-line BRANCH_IF,STATEMENT_REMOVE reason="mid-init failure needs a production seam"
	if err = repo.Storer.SetConfig(cfg); err != nil {
		tb.Fatalf("write repo config in %s: %v", dir, err)
	}

	WriteFile(tb, dir, "README.md", "hello")
	worktree, err := repo.Worktree()
	// gomutants:disable-next-line BRANCH_IF reason="mid-init failure needs a production seam"
	if err != nil {
		tb.Fatalf("open worktree in %s: %v", dir, err)
	}
	// gomutants:disable-next-line BRANCH_IF reason="mid-init failure needs a production seam"
	if _, err = worktree.Add("README.md"); err != nil {
		tb.Fatalf("stage README.md in %s: %v", dir, err)
	}

	commitOptions := git.CommitOptions{
		Author:            &object.Signature{Name: "Test User", Email: "test@example.com", When: time.Now()},
		Committer:         nil,
		Parents:           nil,
		SignKey:           nil,
		Signer:            nil,
		All:               false,
		AllowEmptyCommits: false,
		Amend:             false,
	}
	// gomutants:disable-next-line BRANCH_IF reason="mid-init failure needs a production seam"
	if _, err = worktree.Commit("initial", &commitOptions); err != nil {
		tb.Fatalf("commit initial in %s: %v", dir, err)
	}
	return dir
}

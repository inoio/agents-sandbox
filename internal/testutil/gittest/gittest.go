// Package gittest builds git fixtures for a Go test harness without executing
// the ambient git binary. Every operation goes through go-git, so a fixture can
// neither read the developer's global configuration nor prompt for a signing
// key, and a default-suite test needs no //testenv:allow marker.
//
// A fixture that must reproduce a patch-id match commits the same diff rather
// than cherry-picking: it checks out the target branch, writes the source
// commit's changed file contents, and commits. The resulting diff is identical,
// so `git patch-id` matches without a cherry-pick.
//
//deadcode:ignore reason="test support: referenced only by _test.go files"
package gittest

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	formatcfg "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const (
	testAuthorName  = "Test User"
	testAuthorEmail = "test@example.com"
)

// Repo is a throwaway worktree repository with an optional bare origin.
type Repo struct {
	tb     testing.TB
	dir    string
	repo   *git.Repository
	origin string
}

// New creates a worktree repository in a temp dir, configured with a user and
// the main default branch. It has no commit until the first Commit call.
func New(tb testing.TB) *Repo {
	tb.Helper()
	dir := tb.TempDir()
	return &Repo{tb: tb, dir: dir, repo: initRepository(tb, dir, false), origin: ""}
}

// Dir returns the working-tree directory.
func (r *Repo) Dir() string {
	return r.dir
}

// Write writes content to name (relative to the repo root), creating parent
// directories as needed. The change is not staged until the next Commit.
func (r *Repo) Write(name, content string) {
	r.tb.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		r.tb.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		r.tb.Fatalf("write %s: %v", name, err)
	}
}

// Remove deletes name from the working tree; the next Commit stages the
// deletion.
func (r *Repo) Remove(name string) {
	r.tb.Helper()
	if err := os.Remove(filepath.Join(r.dir, name)); err != nil {
		r.tb.Fatalf("remove %s: %v", name, err)
	}
}

// Rename moves from to to in the worktree and stages both endpoints, the shape
// `git mv` leaves: a rename git's diff collapses to the destination unless
// --no-renames is given.
func (r *Repo) Rename(from, to string) {
	r.tb.Helper()
	destination := filepath.Join(r.dir, to)
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		r.tb.Fatalf("create %s: %v", filepath.Dir(destination), err)
	}
	if _, err := r.worktree().Move(from, to); err != nil {
		r.tb.Fatalf("rename %s -> %s: %v", from, to, err)
	}
}

// Commit stages every change and commits it on the current branch, returning the
// new commit's hash. An empty commit is allowed, so a fixture can build one.
func (r *Repo) Commit(message string) plumbing.Hash {
	r.tb.Helper()
	worktree := r.worktree()
	if err := worktree.AddWithOptions(&git.AddOptions{All: true, Path: "", Glob: "", SkipStatus: false}); err != nil {
		r.tb.Fatalf("stage changes: %v", err)
	}
	signature := r.signature()
	//nolint:exhaustruct_v5 // the fixture pins author and emptiness; the other options keep git defaults
	hash, err := worktree.Commit(message, &git.CommitOptions{
		Author:            signature,
		AllowEmptyCommits: true,
	})
	if err != nil {
		r.tb.Fatalf("commit %q: %v", message, err)
	}
	return hash
}

// Head returns the commit the current branch points at.
func (r *Repo) Head() (plumbing.Hash, error) {
	ref, err := r.repo.Head()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return ref.Hash(), nil
}

// SetBranch points branch name (e.g. "release/0.1") at the given commit,
// creating it if needed.
func (r *Repo) SetBranch(name string, at plumbing.Hash) {
	r.tb.Helper()
	ref := plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), at)
	if err := r.repo.Storer.SetReference(ref); err != nil {
		r.tb.Fatalf("set branch %s: %v", name, err)
	}
}

// Checkout switches the working tree to an existing local branch, discarding
// any uncommitted change.
func (r *Repo) Checkout(name string) {
	r.tb.Helper()
	worktree := r.worktree()
	err := worktree.Checkout(&git.CheckoutOptions{
		Hash:                      plumbing.ZeroHash,
		Branch:                    plumbing.NewBranchReferenceName(name),
		Create:                    false,
		Force:                     true,
		Keep:                      false,
		SparseCheckoutDirectories: nil,
	})
	if err != nil {
		r.tb.Fatalf("checkout %s: %v", name, err)
	}
}

// MergeCommit records a two-parent merge commit on the current branch with
// parents [into, from] and from's tree, the shape `git merge --no-ff` produces
// when `from` is ahead of `into`. It returns the merge commit's hash.
func (r *Repo) MergeCommit(message string, into, from plumbing.Hash) plumbing.Hash {
	r.tb.Helper()
	fromCommit, err := r.repo.CommitObject(from)
	if err != nil {
		r.tb.Fatalf("read merge source %s: %v", from, err)
	}
	author := r.signature()
	merged := &object.Commit{
		Hash:         plumbing.ZeroHash,
		Author:       *author,
		Committer:    *author,
		MergeTag:     "",
		PGPSignature: "",
		Message:      message,
		TreeHash:     fromCommit.TreeHash,
		ParentHashes: []plumbing.Hash{into, from},
		Encoding:     object.MessageEncoding(""),
		ExtraHeaders: nil,
	}
	encoded := r.repo.Storer.NewEncodedObject()
	if encodeErr := merged.Encode(encoded); encodeErr != nil {
		r.tb.Fatalf("encode merge commit: %v", encodeErr)
	}
	hash, err := r.repo.Storer.SetEncodedObject(encoded)
	if err != nil {
		r.tb.Fatalf("store merge commit: %v", err)
	}
	worktree := r.worktree()
	if err := worktree.Reset(&git.ResetOptions{Commit: hash, Mode: git.HardReset, Files: nil}); err != nil {
		r.tb.Fatalf("reset to merge commit: %v", err)
	}
	return hash
}

// AddBareOrigin creates a bare repository, registers it as the remote "origin",
// and returns its path.
func (r *Repo) AddBareOrigin() string {
	r.tb.Helper()
	origin := filepath.Join(r.tb.TempDir(), "origin.git")
	initRepository(r.tb, origin, true)
	//nolint:exhaustruct_v5 // only name, URL, and refspec describe the fixture's origin; the rest stay zero
	if _, err := r.repo.CreateRemote(&config.RemoteConfig{
		Name:  "origin",
		URLs:  []string{origin},
		Fetch: []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
	}); err != nil {
		r.tb.Fatalf("create origin remote: %v", err)
	}
	r.origin = origin
	return origin
}

// Push pushes every local branch to origin.
func (r *Repo) Push(tb testing.TB) {
	tb.Helper()
	//nolint:exhaustruct_v5 // the fixture pushes every branch; the transport options keep their defaults
	err := r.repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"+refs/heads/*:refs/heads/*"},
	})
	if err != nil {
		tb.Fatalf("push to origin: %v", err)
	}
}

// RefExists reports whether a fully-qualified ref (e.g.
// "refs/heads/backport/7-0.1") resolves in the worktree.
func (r *Repo) RefExists(ref string) bool {
	r.tb.Helper()
	_, err := r.repo.Reference(plumbing.ReferenceName(ref), false)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return false
		}
		r.tb.Fatalf("resolve ref %s: %v", ref, err)
	}
	return true
}

// FileAt returns the content of path in the commit a local branch points at.
func (r *Repo) FileAt(branch, path string) (string, bool) {
	r.tb.Helper()
	return fileAt(r.tb, r.repo, branch, path)
}

// signature returns a fixed fixture author signature.
func (r *Repo) signature() *object.Signature {
	return &object.Signature{Name: testAuthorName, Email: testAuthorEmail, When: time.Now()}
}

// worktree opens the repository's worktree.
func (r *Repo) worktree() *git.Worktree {
	r.tb.Helper()
	worktree, err := r.repo.Worktree()
	if err != nil {
		r.tb.Fatalf("open worktree in %s: %v", r.dir, err)
	}
	return worktree
}

// initRepository initializes a repository at dir, configuring a user for a
// worktree (a bare repository carries no user).
func initRepository(tb testing.TB, dir string, bare bool) *git.Repository {
	tb.Helper()
	//nolint:modernize // the embedded InitOptions key is what exhaustruct needs
	initOptions := git.PlainInitOptions{
		InitOptions:  git.InitOptions{DefaultBranch: plumbing.Main},
		Bare:         bare,
		ObjectFormat: formatcfg.ObjectFormat(""),
	}
	repo, err := git.PlainInitWithOptions(dir, &initOptions)
	if err != nil {
		tb.Fatalf("init repo in %s: %v", dir, err)
	}
	if bare {
		return repo
	}
	cfg, err := repo.Config()
	if err != nil {
		tb.Fatalf("read repo config in %s: %v", dir, err)
	}
	cfg.User.Name = testAuthorName
	cfg.User.Email = testAuthorEmail
	if err = repo.Storer.SetConfig(cfg); err != nil {
		tb.Fatalf("write repo config in %s: %v", dir, err)
	}
	return repo
}

// openRepository opens a repository at dir.
func openRepository(tb testing.TB, dir string) *git.Repository {
	tb.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		tb.Fatalf("open repo at %s: %v", dir, err)
	}
	return repo
}

// fileAt returns the content of path in the commit a branch points at.
func fileAt(tb testing.TB, repo *git.Repository, branch, path string) (string, bool) {
	tb.Helper()
	hash, ok := resolveBranch(tb, repo, branch)
	if !ok {
		return "", false
	}
	commit, err := repo.CommitObject(hash)
	if err != nil {
		tb.Fatalf("read commit %s: %v", hash, err)
	}
	file, err := commit.File(path)
	if err != nil {
		return "", false
	}
	content, err := file.Contents()
	if err != nil {
		tb.Fatalf("read %s at %s: %v", path, branch, err)
	}
	return content, true
}

// resolveBranch resolves a local branch name to its commit hash.
func resolveBranch(tb testing.TB, repo *git.Repository, branch string) (plumbing.Hash, bool) {
	tb.Helper()
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(branch), false)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return plumbing.ZeroHash, false
		}
		tb.Fatalf("resolve branch %s: %v", branch, err)
	}
	return ref.Hash(), true
}

// commitsInRange returns the commits reachable from to but not from from, the
// set `git log <from>..<to>` lists.
func commitsInRange(tb testing.TB, repo *git.Repository, from, to plumbing.Hash) []*object.Commit {
	tb.Helper()
	excluded := reachableCommits(tb, repo, from)
	var out []*object.Commit
	//nolint:exhaustruct_v5 // only From selects the walked history; the rest keep defaults
	iter, err := repo.Log(&git.LogOptions{From: to})
	if err != nil {
		tb.Fatalf("log from %s: %v", to, err)
	}
	for {
		commit, err := iter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			tb.Fatalf("walk log from %s: %v", to, err)
		}
		if !excluded[commit.Hash] {
			out = append(out, commit)
		}
	}
	return out
}

// reachableCommits returns the set of commits reachable from from, inclusive.
func reachableCommits(tb testing.TB, repo *git.Repository, from plumbing.Hash) map[plumbing.Hash]bool {
	tb.Helper()
	excluded := map[plumbing.Hash]bool{}
	//nolint:exhaustruct_v5 // only From selects the walked history; the rest keep defaults
	iter, err := repo.Log(&git.LogOptions{From: from})
	if err != nil {
		tb.Fatalf("log from %s: %v", from, err)
	}
	for {
		commit, err := iter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			tb.Fatalf("walk log from %s: %v", from, err)
		}
		excluded[commit.Hash] = true
	}
	return excluded
}

// OriginRefs returns the fully-qualified ref names in a bare origin, sorted.
func OriginRefs(tb testing.TB, origin string) []string {
	tb.Helper()
	repo := openRepository(tb, origin)
	iter, err := repo.References()
	if err != nil {
		tb.Fatalf("list refs in %s: %v", origin, err)
	}
	var refs []string
	collectErr := iter.ForEach(func(ref *plumbing.Reference) error {
		refs = append(refs, ref.Name().String())
		return nil
	})
	if collectErr != nil {
		tb.Fatalf("walk refs in %s: %v", origin, collectErr)
	}
	sort.Strings(refs)
	return refs
}

// OriginFile returns the content of path in the commit a branch points at in a
// bare origin.
func OriginFile(tb testing.TB, origin, branch, path string) (string, bool) {
	tb.Helper()
	return fileAt(tb, openRepository(tb, origin), branch, path)
}

// OriginMessages returns the commit messages in the range from (exclusive) to
// to (inclusive) in a bare origin, newest first.
func OriginMessages(tb testing.TB, origin, from, to string) []string {
	tb.Helper()
	repo := openRepository(tb, origin)
	fromHash, ok := resolveBranch(tb, repo, from)
	if !ok {
		tb.Fatalf("branch %s not found in %s", from, origin)
	}
	toHash, ok := resolveBranch(tb, repo, to)
	if !ok {
		tb.Fatalf("branch %s not found in %s", to, origin)
	}
	var messages []string
	for _, commit := range commitsInRange(tb, repo, fromHash, toHash) {
		messages = append(messages, commit.Message)
	}
	return messages
}

// OriginCount returns the number of commits in the range from (exclusive) to to
// (inclusive) in a bare origin.
func OriginCount(tb testing.TB, origin, from, to string) int {
	tb.Helper()
	repo := openRepository(tb, origin)
	fromHash, ok := resolveBranch(tb, repo, from)
	if !ok {
		tb.Fatalf("branch %s not found in %s", from, origin)
	}
	toHash, ok := resolveBranch(tb, repo, to)
	if !ok {
		tb.Fatalf("branch %s not found in %s", to, origin)
	}
	return len(commitsInRange(tb, repo, fromHash, toHash))
}

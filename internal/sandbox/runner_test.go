package sandbox

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
	"gitlab.inoio.de/inoio/opencode-msb/internal/prompt"
)

func TestParseMemoryGigabytes(t *testing.T) {
	got := parseMemory("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryMegabytes(t *testing.T) {
	got := parseMemory("512M")
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryPlainNumber(t *testing.T) {
	got := parseMemory("2048")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryLowercase(t *testing.T) {
	got := parseMemory("2g")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestSandboxNameFormat(t *testing.T) {
	got := sandboxName("myproj-aBc1234D", "main")
	expected := "opencode-msb-sb-myproj-aBc1234D-main"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxNameTruncation(t *testing.T) {
	got := sandboxName("p-abcdef", "feat-very-long-branch-name-that-exceeds-the-limit-and-more")
	if len(got) > 128 {
		t.Errorf("expected name <= 128 bytes, got %d", len(got))
	}
}

func TestResolveTmpSizeDefaultsWhenEmpty(t *testing.T) {
	got := resolveTmpSizeMiB("")
	if got != defaultTmpSizeMiB {
		t.Errorf("expected default %d, got %d", defaultTmpSizeMiB, got)
	}
}

func TestResolveTmpSizeParsesSpec(t *testing.T) {
	got := resolveTmpSizeMiB("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestBuildMountsIncludesTmpfsAtTmp(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", defaultTmpSizeMiB)

	tmpMount, ok := mounts["/tmp"]
	if !ok {
		t.Fatal("expected /tmp mount, not found in mounts map")
	}
	if tmpMount.Kind() != msb.MountKindTmpfs {
		t.Errorf("expected /tmp to be a tmpfs mount, got kind %d", tmpMount.Kind())
	}
	if tmpMount.SizeMiB == 0 {
		t.Error("expected /tmp tmpfs to have a nonzero size cap")
	}
	if tmpMount.SizeMiB < 1024 {
		t.Errorf("expected /tmp tmpfs to be at least 1 GiB, got %d MiB", tmpMount.SizeMiB)
	}
}

func TestBuildMountsRespectsCustomTmpSize(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", 4096)

	tmpMount := mounts["/tmp"]
	if tmpMount.SizeMiB != 4096 {
		t.Errorf("expected /tmp tmpfs size 4096 MiB, got %d", tmpMount.SizeMiB)
	}
}

func TestBuildEnvMap(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env")
	writeFile(t, envFile, "FOO=bar\n# comment\n\nBAZ=qux\n")

	got := buildEnvMap(envFile)

	if len(got) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(got), got)
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", got["FOO"])
	}
	if got["BAZ"] != "qux" {
		t.Errorf("expected BAZ=qux, got %q", got["BAZ"])
	}
}

func TestReadSandboxEnvMissing(t *testing.T) {
	env := buildEnvMap("missing")
	if len(env) != 0 {
		t.Errorf("expected 0 env vars when .opencode-msb/env missing, got %d", len(env))
	}
}

func TestBuildOpencodeArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		auto bool
		want []string
	}{
		{"auto default", nil, true, []string{autoFlag}},
		{"auto with forwarded args", []string{"foo", "bar"}, true, []string{autoFlag, "foo", "bar"}},
		{"no-auto", []string{"foo"}, false, []string{"foo"}},
		{"no-auto empty args", nil, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOpencodeArgs(tt.args, tt.auto)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildOpencodeArgs(%v, %v) = %v, want %v", tt.args, tt.auto, got, tt.want)
			}
		})
	}
}

func TestResolveWorkspaceNoBranch(t *testing.T) {
	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello")
	branch := currentBranch(t, repo)

	repoPath, gotBranch, cwdBranch, created, err := resolveWorkspace(
		repo,
		RunOptions{},
		Config{StateDir: t.TempDir()},
		"test-project",
		newTestLogger(t),
	)
	if err != nil {
		t.Fatalf("resolveWorkspace failed: %v", err)
	}
	if repoPath != repo {
		t.Errorf("expected workspace %q, got %q", repo, repoPath)
	}
	if gotBranch != branch {
		t.Errorf("expected branch %q, got %q", branch, gotBranch)
	}
	if cwdBranch != "" {
		t.Errorf("expected empty cwdBranch, got %q", cwdBranch)
	}
	if created {
		t.Error("expected created=false")
	}
}

func TestResolveWorkspaceBranchMatchesCurrentBranch(t *testing.T) {
	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello")
	branch := currentBranch(t, repo)

	repoPath, gotBranch, cwdBranch, created, err := resolveWorkspace(
		repo,
		RunOptions{Branch: branch},
		Config{StateDir: t.TempDir()},
		"test-project",
		newTestLogger(t),
	)
	if err != nil {
		t.Fatalf("resolveWorkspace failed: %v", err)
	}
	if repoPath != repo {
		t.Errorf("expected workspace %q, got %q", repo, repoPath)
	}
	if gotBranch != branch {
		t.Errorf("expected branch %q, got %q", branch, gotBranch)
	}
	if cwdBranch != branch {
		t.Errorf("expected cwdBranch %q, got %q", branch, cwdBranch)
	}
	if created {
		t.Error("expected created=false")
	}
}

func TestResolveWorkspaceBranchCreatesManagedRepo(t *testing.T) {
	prompt.AssumeYes = true
	defer func() { prompt.AssumeYes = false }()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello")
	branch := currentBranch(t, repo)

	stateDir := t.TempDir()
	projectSlug := "test-project"
	wantPath := filepath.Join(stateDir, "isolated-workspaces", projectSlug, "feature")

	repoPath, gotBranch, cwdBranch, created, err := resolveWorkspace(
		repo,
		RunOptions{Branch: "feature"},
		Config{StateDir: stateDir},
		projectSlug,
		newTestLogger(t),
	)
	if err != nil {
		t.Fatalf("resolveWorkspace failed: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if gotBranch != "feature" {
		t.Errorf("expected branch feature, got %q", gotBranch)
	}
	if cwdBranch != branch {
		t.Errorf("expected cwdBranch %q, got %q", branch, cwdBranch)
	}
	if repoPath != wantPath {
		t.Errorf("expected managed repo path %q, got %q", wantPath, repoPath)
	}
}

func TestResolveWorkspaceBranchOutsideRepo(t *testing.T) {
	tmp := t.TempDir()

	_, _, _, _, err := resolveWorkspace(
		tmp,
		RunOptions{Branch: "feature"},
		Config{StateDir: t.TempDir()},
		"test-project",
		newTestLogger(t),
	)
	if err == nil {
		t.Fatal("expected error when --branch is used outside a git repo")
	}
}

func TestCleanupManagedRepoRemovesCleanRepo(t *testing.T) {
	prompt.AssumeYes = true
	defer func() { prompt.AssumeYes = false }()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello")
	repoPath := createManagedRepo(t, repo)

	err := cleanupManagedRepo(repoPath, repo, currentBranch(t, repo), RunOptions{Branch: "feature"}, newTestLogger(t))
	if err != nil {
		t.Fatalf("cleanupManagedRepo failed: %v", err)
	}
	if _, statErr := os.Stat(repoPath); !os.IsNotExist(statErr) {
		t.Errorf("expected managed repo %q to be removed", repoPath)
	}
}

func TestCleanupManagedRepoKeepsUncommittedChanges(t *testing.T) {
	prompt.AssumeYes = true
	defer func() { prompt.AssumeYes = false }()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello")
	repoPath := createManagedRepo(t, repo)
	writeFile(t, filepath.Join(repoPath, "feature.txt"), "feature work")

	err := cleanupManagedRepo(repoPath, repo, currentBranch(t, repo), RunOptions{Branch: "feature"}, newTestLogger(t))
	if err != nil {
		t.Fatalf("cleanupManagedRepo failed: %v", err)
	}
	if _, statErr := os.Stat(repoPath); os.IsNotExist(statErr) {
		t.Fatal("expected managed repo to be kept")
	}
	got, err := os.ReadFile(filepath.Join(repoPath, "feature.txt"))
	if err != nil {
		t.Fatalf("reading kept file: %v", err)
	}
	if string(got) != "feature work" {
		t.Errorf("expected kept changes intact, got %q", got)
	}
}

func TestCleanupManagedRepoDiscardsAndRemoves(t *testing.T) {
	cleanup := prompt.SetStdinForTesting(strings.NewReader("d\nr\n"))
	defer cleanup()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello")
	repoPath := createManagedRepo(t, repo)
	writeFile(t, filepath.Join(repoPath, "feature.txt"), "feature work")

	err := cleanupManagedRepo(repoPath, repo, currentBranch(t, repo), RunOptions{Branch: "feature"}, newTestLogger(t))
	if err != nil {
		t.Fatalf("cleanupManagedRepo failed: %v", err)
	}
	if _, statErr := os.Stat(repoPath); !os.IsNotExist(statErr) {
		t.Errorf("expected managed repo %q to be removed", repoPath)
	}
}

func TestCleanupManagedRepoMergeSuccess(t *testing.T) {
	cleanup := prompt.SetStdinForTesting(strings.NewReader("m\n\n"))
	defer cleanup()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello")
	repoPath := createManagedRepo(t, repo)
	writeFile(t, filepath.Join(repoPath, "feature.txt"), "feature work")
	runGit(t, repoPath, "add", ".")
	runGit(t, repoPath, "commit", "-m", "feature commit")

	targetBranch := currentBranch(t, repo)
	err := cleanupManagedRepo(repoPath, repo, targetBranch, RunOptions{Branch: "feature"}, newTestLogger(t))
	if err != nil {
		t.Fatalf("cleanupManagedRepo failed: %v", err)
	}
	if _, statErr := os.Stat(repoPath); !os.IsNotExist(statErr) {
		t.Errorf("expected managed repo %q to be removed", repoPath)
	}
	out := runGitOutput(t, repo, "log", "--oneline", targetBranch)
	if !strings.Contains(out, "feature commit") {
		t.Errorf("feature commit not reachable from %q: %q", targetBranch, out)
	}
}

func TestCleanupManagedRepoMergeConflict(t *testing.T) {
	cleanup := prompt.SetStdinForTesting(strings.NewReader("m\n\n"))
	defer cleanup()

	repo := createTempRepo(t)
	commitFile(t, repo, "file.txt", "main")
	repoPath := createManagedRepo(t, repo)

	writeFile(t, filepath.Join(repoPath, "file.txt"), "feature")
	runGit(t, repoPath, "add", ".")
	runGit(t, repoPath, "commit", "-m", "feature commit")

	writeFile(t, filepath.Join(repo, "file.txt"), "main conflicting")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "main conflicting")

	targetBranch := currentBranch(t, repo)
	err := cleanupManagedRepo(repoPath, repo, targetBranch, RunOptions{Branch: "feature"}, newTestLogger(t))
	if err == nil {
		t.Fatal("expected error for merge conflict")
	}
	if _, statErr := os.Stat(repoPath); !os.IsNotExist(statErr) {
		t.Errorf("expected managed repo %q to be removed", repoPath)
	}
	mergeHead := filepath.Join(repo, ".git", "MERGE_HEAD")
	if _, statErr := os.Stat(mergeHead); !os.IsNotExist(statErr) {
		t.Error("expected merge to be aborted")
	}
}

func TestIsSandboxActive(t *testing.T) {
	tests := []struct {
		name   string
		status msb.SandboxStatus
		want   bool
	}{
		{"running", msb.SandboxStatusRunning, true},
		{"draining", msb.SandboxStatusDraining, true},
		{"paused", msb.SandboxStatusPaused, true},
		{"stopped", msb.SandboxStatusStopped, false},
		{"crashed", msb.SandboxStatusCrashed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSandboxActive(tt.status); got != tt.want {
				t.Errorf("isSandboxActive(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestPromptExistingSessionTerminate(t *testing.T) {
	cleanup := prompt.SetStdinForTesting(strings.NewReader("t\n"))
	defer cleanup()

	got, err := promptExistingSession("opencode-msb-proj-main", newTestLogger(t))
	if err != nil {
		t.Fatalf("promptExistingSession failed: %v", err)
	}
	if !got {
		t.Error("expected terminate=true when user picks 't'")
	}
}

func TestPromptExistingSessionExitDefault(t *testing.T) {
	cleanup := prompt.SetStdinForTesting(strings.NewReader("\n"))
	defer cleanup()

	got, err := promptExistingSession("opencode-msb-proj-main", newTestLogger(t))
	if err != nil {
		t.Fatalf("promptExistingSession failed: %v", err)
	}
	if got {
		t.Error("expected terminate=false (exit) when user accepts the default")
	}
}

func TestPromptExistingSessionNonInteractiveExits(t *testing.T) {
	prompt.AssumeYes = true
	defer func() { prompt.AssumeYes = false }()

	got, err := promptExistingSession("opencode-msb-proj-main", newTestLogger(t))
	if err != nil {
		t.Fatalf("promptExistingSession failed: %v", err)
	}
	if got {
		t.Error("expected terminate=false (exit) in non-interactive mode")
	}
}

func createTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	return dir
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))
}

func createManagedRepo(t *testing.T, repo string) string {
	t.Helper()
	stateDir := t.TempDir()
	repoPath, _, err := git.EnsureManagedRepoFromRef(repo, stateDir, "test-project", "feature", "HEAD")
	if err != nil {
		t.Fatalf("create managed repo: %v", err)
	}
	runGit(t, repoPath, "config", "user.email", "test@example.com")
	runGit(t, repoPath, "config", "user.name", "Test User")
	return repoPath
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func commitFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, relPath), content)
	runGit(t, dir, "add", relPath)
	runGit(t, dir, "commit", "-m", "initial")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v: %s", args, dir, err, out)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v: %s", args, dir, err, out)
	}
	return string(out)
}

func newTestLogger(t *testing.T) *output.Printer {
	t.Helper()
	return output.NewPrinter(io.Discard, false)
}

func TestMergeEnvMapsProjectOverridesUser(t *testing.T) {
	userFile := filepath.Join(t.TempDir(), "env")
	writeFile(t, userFile, "FOO=user\nBAR=user\n")
	projectFile := filepath.Join(t.TempDir(), "env")
	writeFile(t, projectFile, "FOO=project\n")

	got := mergeEnvMaps(buildEnvMap(userFile), buildEnvMap(projectFile))
	want := map[string]string{"FOO": "project", "BAR": "user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSandboxSessionHasCloneVolumeField(t *testing.T) {
	// Verify the struct has a cloneVol field.
	// This is a structural test — full lifecycle requires msb.
	s := &sandboxSession{
		name:     "test-sandbox",
		cloneVol: "test-clone-vol",
	}
	if s.cloneVol != "test-clone-vol" {
		t.Errorf("expected cloneVol to be set, got %q", s.cloneVol)
	}
}

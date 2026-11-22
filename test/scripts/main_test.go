package scripts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	Register("gh", fakeGhHandler)
	Register("asb", surfaceSweepAsbHandler)
	Register("docker", surfaceSweepDockerHandler)
	Register("virsh", kvmGuestVirshHandler)
	Register("ssh", kvmGuestSSHHandler)
	Register("rsync", kvmGuestRsyncHandler)
	Register("git", kvmGuestGitHandler)
	Register("nft", kvmGuestNftHandler)
	Register("id", kvmGuestIDHandler)
	Register("faketool", func(args []string) (string, string, int) {
		return "out:" + strings.Join(args, ","), "err\n", 3
	})
	// A fake exits through syscall.Exit, not os.Exit: this harness package has
	// only _test.go files, so its test binary carries no coverage metadata and
	// the Go coverage runtime's exit hook would print "program not built with
	// -cover" to the fake's stderr under `go test -cover` (the codecov and
	// mutation runs). The raw exit skips that hook.
	if status := Dispatch(); status >= 0 {
		syscall.Exit(status)
	}
	mocks.InitFailFastMocks(m)
}

// TestTrackScriptInputs makes every tracked shell script and the Makefile part of
// this package's test-cache key. The harnesses run their scripts as subprocesses,
// whose file opens the test process cannot observe (RunScript reads the script
// under test for the same reason), so without this a change to an invoked script
// or to the Makefile a harness drives could replay a cached result. Reading them
// here records each through internal/testlog; a change to any input invalidates
// the whole package, and a deleted one merely re-runs rather than failing.
func TestTrackScriptInputs(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	inputs := []string{filepath.Join(root, "Makefile")}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".sh") {
			inputs = append(inputs, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	for _, path := range inputs {
		if _, err := os.ReadFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read %s: %v", path, err)
		}
	}
	if len(inputs) < 2 {
		t.Fatalf("found %d inputs under %s; expected the Makefile and at least one script", len(inputs), root)
	}
}

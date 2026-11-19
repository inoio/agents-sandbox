// Package fakecmd fakes an external CLI a script under test calls without a
// shell stub. The test puts a directory holding a symlink to the test binary on
// PATH; when the script invokes the command the test binary runs again, and its
// TestMain serves the canned response through Dispatch before running any test.
//
//deadcode:ignore reason="test support: referenced only by _test.go files"
package fakecmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Handler serves one faked command invocation: it receives the command's
// arguments and returns the streams to write and the exit status.
type Handler func(args []string) (stdout, stderr string, status int)

var (
	handlersMu sync.Mutex
	handlers   = map[string]Handler{}
)

// Register associates a faked command name with its handler. It must be called
// from TestMain, before Dispatch, so the re-executed process — which starts with
// an empty registry — registers the same handlers before it serves one.
func Register(name string, handler Handler) {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	handlers[name] = handler
}

// BinDir returns a temp directory holding an executable name that symlinks to
// the test binary. Putting the directory on the script's PATH makes the script
// invoke the test binary in place of the real command.
func BinDir(tb testing.TB, name string) string {
	tb.Helper()
	executable, err := os.Executable()
	if err != nil {
		tb.Fatalf("locate test binary: %v", err)
	}
	dir := tb.TempDir()
	if err := os.Symlink(executable, filepath.Join(dir, name)); err != nil {
		tb.Fatalf("link fake %s: %v", name, err)
	}
	return dir
}

// Dispatch serves the handler registered for the name this process was invoked
// as, returning -1 when it is not a fake. A test package's TestMain calls it
// first, registering its fakes before it does:
//
//	func TestMain(m *testing.M) {
//		fakecmd.Register("gh", fakeGh)
//		if status := fakecmd.Dispatch(); status >= 0 {
//			os.Exit(status)
//		}
//		mocks.InitFailFastMocks(m)
//	}
func Dispatch() int {
	name := filepath.Base(os.Args[0])
	handlersMu.Lock()
	handler, ok := handlers[name]
	handlersMu.Unlock()
	if !ok {
		return -1
	}
	stdout, stderr, status := handler(os.Args[1:])
	fmt.Fprint(os.Stdout, stdout)
	fmt.Fprint(os.Stderr, stderr)
	return status
}

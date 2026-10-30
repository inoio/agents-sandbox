package opener

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// hostOpenSettleTime is how long OpenOnHost waits for the host handler to exit.
// open and xdg-open usually hand the target to the desktop and return at once,
// but on some Linux desktops xdg-open runs a freshly started browser in the
// foreground. A handler still running after this time is treated as success and
// left running: killing it would close the user's browser.
// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="a settle window, not a contract; tests override it, so the default's exact value is unobservable"
var hostOpenSettleTime = 3 * time.Second

// execCommand is a test seam; production uses exec.Command.
var execCommand = exec.Command

// InstallFailFast points execCommand at a panic so a test that reaches the real
// host opener without overriding the seam fails loud instead of opening a
// browser. Wired up by the package TestMain and testutil.InitFailFastMocks.
var InstallFailFast = func() { execCommand = failFastExecCommand }

func failFastExecCommand(name string, _ ...string) *exec.Cmd {
	panic("opener: reached real execCommand(" + name + "); override execCommand in the test")
}

// hostOpenCommand returns the host's default-handler command for goos. The
// target is passed as a single argv element and never through a shell.
// ClassifyTarget guarantees it starts with a scheme, so neither command can
// mistake it for an option.
func hostOpenCommand(goos string) string {
	if goos == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// OpenOnHost opens target with the host's default handler. The handler's output
// is discarded: capturing it would make the wait hang on a browser that
// inherits the pipe.
func OpenOnHost(target string) error {
	command := hostOpenCommand(runtime.GOOS)
	process := execCommand(command, target)
	if err := process.Start(); err != nil {
		return fmt.Errorf("start %s: %w", command, err)
	}
	// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="the buffer only keeps the Wait goroutine from blocking after the caller times out; its capacity is unobservable"
	exited := make(chan error, 1)
	go func() { exited <- process.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("%s: %w", command, err)
		}
		return nil
	case <-time.After(hostOpenSettleTime):
		return nil
	}
}

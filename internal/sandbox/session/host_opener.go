package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// resolveHomeVolumePath returns the host directory backing a home volume. It is
// a test seam; production asks microsandbox for the volume's path.
var resolveHomeVolumePath = func(ctx context.Context, volumeName string) (string, error) {
	handle, err := msb.Get().GetVolume(ctx, volumeName)
	if err != nil {
		return "", fmt.Errorf("look up home volume %q: %w", volumeName, err)
	}
	return handle.Path(), nil
}

// watchHostOpener serves the in-VM xdg-open shim's spool until ctx is done. It
// is a test seam; production runs an opener.Watcher with the host handler,
// forwarding OAuth login callbacks into sb.
var watchHostOpener = func(ctx context.Context, homeVolumePath string, sb msb.Sandbox) (opener.Report, error) {
	watcher := &opener.Watcher{
		HomeVolumePath: homeVolumePath,
		Handle:         opener.NewHostHandler(opener.OpenOnHost, opener.NewCallbackForwarder(guestFetcher(sb))),
	}
	return watcher.Watch(ctx)
}

// curlExitCouldNotConnect is curl's exit status when nothing accepts the
// connection.
const curlExitCouldNotConnect = 7

// guestFetcher replays a login callback inside the VM with curl over the msb
// exec channel, so it works without guest egress. Agents bind their callback
// on 127.0.0.1 or, for some, on ::1, so a refused IPv4 connection is retried
// on IPv6. Errors carry only the exit status: the URL holds the authorization
// code and must not end up in a log.
func guestFetcher(sb msb.Sandbox) opener.GuestFetcher {
	return func(ctx context.Context, url string) ([]byte, error) {
		result, err := fetchInGuest(ctx, sb, url)
		if err == nil && result.ExitCode() == curlExitCouldNotConnect {
			result, err = fetchInGuest(ctx, sb, strings.Replace(url, "://127.0.0.1:", "://[::1]:", 1))
		}
		if err != nil {
			return nil, errors.New("replaying the login callback in the VM failed")
		}
		if !result.Success() {
			return nil, fmt.Errorf("replaying the login callback in the VM failed: curl exit %d", result.ExitCode())
		}
		return result.StdoutBytes(), nil
	}
}

// curlCallbackArguments makes curl print the response exactly as received:
// --include adds the status line and headers, and --raw keeps transfer
// encodings such as chunked framing intact, so the host decodes the body once.
func curlCallbackArguments(url string) []string {
	return []string{"--silent", "--include", "--raw", "--noproxy", "*", "--max-time", "110", "--", url}
}

func fetchInGuest(ctx context.Context, sb msb.Sandbox, url string) (msb.ShellResult, error) {
	return sb.Exec(ctx, "curl", curlCallbackArguments(url))
}

// startHostOpener runs the host side of the in-VM xdg-open shim for the
// session and returns a stop function. Like the notify watcher, it runs only
// while this launcher is attached; requests made while no launcher is attached
// time out in the shim. Nothing is printed while the TUI is attached: stop
// reports refused targets afterwards, when the terminal is the user's again.
func startHostOpener(ctx context.Context, ses preparedSandbox, enabled bool, ui termio.UI) func() {
	sb := ses.Sandbox()
	if !enabled || sb == nil || ses.HomeVolume() == "" {
		ui.Verbosef("not starting host opener, enabled %v, sandbox nil %v", enabled, sb == nil)
		return func() {}
	}
	homeVolumePath, err := resolveHomeVolumePath(ctx, ses.HomeVolume())
	if err != nil || homeVolumePath == "" {
		ui.Verbosef("not starting host opener: home volume path unavailable: %v", err)
		return func() {}
	}
	watchContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var report opener.Report
	var watchErr error
	go func() {
		defer close(done)
		report, watchErr = watchHostOpener(watchContext, homeVolumePath, sb)
	}()
	return func() {
		cancel()
		<-done
		reportHostOpener(report, watchErr, ui)
	}
}

// reportHostOpener lists what the host opener refused during the session, so
// the user can open those targets by hand.
func reportHostOpener(report opener.Report, watchErr error, ui termio.UI) {
	if watchErr != nil {
		ui.Verbosef("host opener: %v", watchErr)
	}
	for _, refusal := range report.Refusals {
		ui.Infof("not opened on the host: %s (%s)", refusal.Target, refusal.Reason)
	}
	if report.Omitted > 0 {
		ui.Infof("not opened on the host: %d more", report.Omitted)
	}
}

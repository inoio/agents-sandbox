package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

func TestGuestFetcherReplaysCallbackWithCurl(t *testing.T) {
	var calls []string
	sb := &msb.MockSandbox{ExecCalls: &calls}
	url := "http://127.0.0.1:43117/callback?code=secret"
	key := "curl --silent --include --raw --noproxy * --max-time 110 -- " + url
	sb.ExecOut = map[string]msb.ShellResult{key: msb.NewTestResult(true, 0, "HTTP/1.1 200 OK\r\n\r\n", "", nil)}

	raw, err := guestFetcher(sb)(context.Background(), url)

	if err != nil || string(raw) != "HTTP/1.1 200 OK\r\n\r\n" {
		t.Fatalf("raw %q, err %v", raw, err)
	}
	if len(calls) != 1 || calls[0] != key {
		t.Fatalf("calls %q", calls)
	}
}

func TestGuestFetcherRetriesOnIPv6WhenIPv4IsRefused(t *testing.T) {
	var calls []string
	sb := &msb.MockSandbox{ExecCalls: &calls}
	prefix := "curl --silent --include --raw --noproxy * --max-time 110 -- "
	sb.ExecOut = map[string]msb.ShellResult{
		prefix + "http://127.0.0.1:1455/auth/callback?code=c": msb.NewTestResult(false, 7, "", "", nil),
		prefix + "http://[::1]:1455/auth/callback?code=c": msb.NewTestResult(
			true,
			0,
			"HTTP/1.1 200 OK\r\n\r\n",
			"",
			nil,
		),
	}

	if _, err := guestFetcher(sb)(context.Background(), "http://127.0.0.1:1455/auth/callback?code=c"); err != nil {
		t.Fatalf("err %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls %q", calls)
	}
}

// TestGuestFetcherRetriesOnlyTheCallbackHost covers the IPv6 retry rewriting
// just the first 127.0.0.1, not a later occurrence in the query.
func TestGuestFetcherRetriesOnlyTheCallbackHost(t *testing.T) {
	var calls []string
	sb := &msb.MockSandbox{ExecCalls: &calls}
	prefix := "curl --silent --include --raw --noproxy * --max-time 110 -- "
	url := "http://127.0.0.1:1455/auth/callback?u=http://127.0.0.1:9999/x"
	retried := "http://[::1]:1455/auth/callback?u=http://127.0.0.1:9999/x"
	sb.ExecOut = map[string]msb.ShellResult{
		prefix + url:     msb.NewTestResult(false, 7, "", "", nil),
		prefix + retried: msb.NewTestResult(true, 0, "HTTP/1.1 200 OK\r\n\r\n", "", nil),
	}

	if _, err := guestFetcher(sb)(context.Background(), url); err != nil {
		t.Fatalf("err %v", err)
	}
	if len(calls) != 2 || calls[1] != prefix+retried {
		t.Fatalf("calls %q, want only the callback host rewritten", calls)
	}
}

func TestGuestFetcherErrorsNeverContainTheCallbackURL(t *testing.T) {
	url := "http://127.0.0.1:1455/cb?code=secret"
	failing := &msb.MockSandbox{ExecOut: map[string]msb.ShellResult{
		"curl --silent --include --raw --noproxy * --max-time 110 -- " + url: msb.NewTestResult(
			false,
			28,
			"",
			"timeout "+url,
			nil,
		),
	}}
	broken := &msb.MockSandbox{ExecErr: errors.New("exec failed for " + url)}
	for _, sb := range []*msb.MockSandbox{failing, broken} {
		_, err := guestFetcher(sb)(context.Background(), url)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestStartHostOpenerWatchesHomeVolumeAndReportsAfterStop(t *testing.T) {
	report := opener.Report{
		Refusals: []opener.Refusal{{Target: "file:///etc/passwd", Reason: "scheme not allowed"}},
		Omitted:  2,
	}
	watched := stubHostOpener(t, "/host/volumes/home-vol", report, errors.New("boom"))
	ui := &termiotest.Mock{}
	ses := &fakePrepared{sb: &msb.MockSandbox{}, homeVolume: "home-vol"}

	stop := startHostOpener(context.Background(), ses, true, ui)
	if len(ui.InfoCalls) != 0 {
		t.Fatalf("printed while attached: %v", ui.InfoCalls)
	}
	stop()

	if len(*watched) != 1 || (*watched)[0] != "/host/volumes/home-vol" {
		t.Fatalf("watched %v", *watched)
	}
	joined := strings.Join(ui.InfoCalls, "\n")
	if !strings.Contains(joined, "file:///etc/passwd (scheme not allowed)") || !strings.Contains(joined, "2 more") {
		t.Fatalf("info calls = %v", ui.InfoCalls)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "boom") {
		t.Fatalf("watch error not logged: %v", ui.VerboseCalls)
	}
}

func TestStartHostOpenerSkipsWhenDisabledOrWithoutVM(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		enabled bool
		ses     *fakePrepared
	}{
		{"disabled", false, &fakePrepared{sb: &msb.MockSandbox{}, homeVolume: "home-vol"}},
		{"no sandbox", true, &fakePrepared{homeVolume: "home-vol"}},
		{"no home volume", true, &fakePrepared{sb: &msb.MockSandbox{}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			watched := stubHostOpener(t, "/host/volumes/home-vol", opener.Report{}, nil)
			ui := &termiotest.Mock{}
			startHostOpener(context.Background(), testCase.ses, testCase.enabled, ui)()
			if len(*watched) != 0 {
				t.Fatalf("watcher started: %v", *watched)
			}
			if testCase.name == "no sandbox" &&
				!strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "sandbox nil true") {
				t.Fatalf("verbose calls = %v, want the skip reason to name the missing sandbox", ui.VerboseCalls)
			}
		})
	}
}

// TestStartHostOpenerSkipsWhenVolumePathLookupFails covers a lookup that fails
// while still returning a path: it must not start the watcher.
func TestStartHostOpenerSkipsWhenVolumePathLookupFails(t *testing.T) {
	watched := stubHostOpener(t, "/host/volumes/home-vol", opener.Report{}, nil)
	resolveHomeVolumePath = func(context.Context, string) (string, error) {
		return "/host/volumes/home-vol", errors.New("lookup failed")
	}
	startHostOpener(
		context.Background(),
		&fakePrepared{sb: &msb.MockSandbox{}, homeVolume: "home-vol"},
		true,
		&termiotest.Mock{},
	)()
	if len(*watched) != 0 {
		t.Fatalf("watcher started despite a lookup error: %v", *watched)
	}
}

func TestStartHostOpenerSkipsWhenVolumePathUnavailable(t *testing.T) {
	watched := stubHostOpener(t, "", opener.Report{}, nil)
	ui := &termiotest.Mock{}
	startHostOpener(context.Background(), &fakePrepared{sb: &msb.MockSandbox{}, homeVolume: "home-vol"}, true, ui)()
	if len(*watched) != 0 {
		t.Fatalf("watcher started: %v", *watched)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "home volume path unavailable") {
		t.Fatalf("verbose calls = %v", ui.VerboseCalls)
	}
}

// TestReportHostOpenerOmittedBoundaries pins the omission report: a zero count
// says nothing, a positive one counts.
func TestReportHostOpenerOmittedBoundaries(t *testing.T) {
	ui := &termiotest.Mock{}
	reportHostOpener(opener.Report{Omitted: 1}, nil, ui)
	if !strings.Contains(strings.Join(ui.InfoCalls, "\n"), "1 more") {
		t.Fatalf("info calls = %v, want the one omitted target", ui.InfoCalls)
	}
	ui = &termiotest.Mock{}
	reportHostOpener(opener.Report{Omitted: 0}, nil, ui)
	if strings.Contains(strings.Join(ui.InfoCalls, "\n"), "more") {
		t.Fatalf("info calls = %v, want nothing for zero omissions", ui.InfoCalls)
	}
}

func TestResolveHomeVolumePathUsesVolumeHandle(t *testing.T) {
	client := &msb.MockMsbClient{}
	client.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name, Path_: "/host/" + name}, nil
	}
	msb.WithMsbMock(t, client)
	got, err := resolveHomeVolumePath(context.Background(), "home-vol")
	if err != nil || got != "/host/home-vol" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveHomeVolumePathReportsLookupFailure(t *testing.T) {
	sentinel := errors.New("no such volume")
	client := &msb.MockMsbClient{}
	client.SetGetVolumeErr(sentinel)
	msb.WithMsbMock(t, client)
	_, err := resolveHomeVolumePath(
		context.Background(),
		"home-vol",
	)
	if err == nil || !strings.Contains(err.Error(), "home-vol") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want it to wrap the lookup error", err)
	}
}

// TestWatchHostOpenerServesUntilCancelled runs the production watch seam on an
// empty home volume: it must start and stop cleanly without opening anything.
func TestWatchHostOpenerServesUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	report, err := watchHostOpener(ctx, t.TempDir(), &msb.MockSandbox{})
	if err != nil || len(report.Refusals) != 0 {
		t.Fatalf("report %+v, err %v", report, err)
	}
}

package opener

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newSpoolDirectory(t *testing.T) (string, string) {
	t.Helper()
	homeVolume := t.TempDir()
	spoolDirectory := filepath.Join(homeVolume, SpoolRelativePath)
	if err := os.MkdirAll(spoolDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	return homeVolume, spoolDirectory
}

// TestSpoolEntriesReportsReadError covers the ReadDir failure branch: a
// directory opened with O_DIRECTORY cannot otherwise fail to read in a test, so
// the read goes through a seam.
func TestSpoolEntriesReportsReadError(t *testing.T) {
	homeVolume, _ := newSpoolDirectory(t)
	spoolDirectory, err := openSpool(homeVolume)
	if err != nil {
		t.Fatalf("openSpool: %v", err)
	}
	defer spoolDirectory.close()

	original := readDirectoryEntries
	readDirectoryEntries = func(*os.File) ([]os.DirEntry, error) {
		return nil, errors.New("read directory")
	}
	t.Cleanup(func() { readDirectoryEntries = original })

	if _, err := spoolDirectory.entries(); err == nil {
		t.Fatal("entries swallowed a directory read error")
	}
}

// TestSpoolEntriesRejectsGuestFIFO covers a guest that replaces the spool
// directory with a FIFO: a plain blocking open would wait forever for a writer
// and hang the watcher, and session shutdown waits for the watcher.
func TestSpoolEntriesRejectsGuestFIFO(t *testing.T) {
	homeVolume := t.TempDir()
	fifoPath := filepath.Join(homeVolume, SpoolRelativePath)
	if err := os.MkdirAll(filepath.Dir(fifoPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	spoolDirectory, err := openSpool(homeVolume)
	if err != nil {
		t.Fatalf("openSpool: %v", err)
	}
	defer spoolDirectory.close()

	listed := make(chan error, 1)
	go func() {
		_, err := spoolDirectory.entries()
		listed <- err
	}()
	select {
	case err := <-listed:
		if err == nil {
			t.Fatal("entries accepted a FIFO spool directory")
		}
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Fatalf("err = %v, want ENOTDIR from the O_DIRECTORY open", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("entries blocked on a FIFO spool directory")
	}
}

func writeRequest(t *testing.T, spoolDirectory, identifier, target string) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(spoolDirectory, identifier+requestSuffix),
		[]byte(target+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
}

func readResponse(t *testing.T, spoolDirectory, identifier string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(spoolDirectory, identifier+responseSuffix))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return string(content)
}

func pollOnce(t *testing.T, watcher *Watcher, homeVolume string) Report {
	t.Helper()
	spoolDirectory, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolDirectory.close()
	watcher.poll(context.Background(), spoolDirectory)
	watcher.serving.Wait()
	return watcher.report
}

func TestWatcherOpensAllowedTargetAndResponds(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	var opened []string
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(_ context.Context, target string) error {
		opened = append(opened, target)
		return nil
	}}
	writeRequest(t, spoolDirectory, "abc123", "https://example.com")

	report := pollOnce(t, watcher, homeVolume)

	if len(opened) != 1 || opened[0] != "https://example.com" {
		t.Fatalf("opened = %q", opened)
	}
	if got := readResponse(t, spoolDirectory, "abc123"); got != "ok\n" {
		t.Fatalf("response = %q", got)
	}
	if len(report.Refusals) != 0 {
		t.Fatalf("unexpected refusals %+v", report.Refusals)
	}
	if _, err := os.Stat(filepath.Join(spoolDirectory, "abc123"+claimedSuffix)); !os.IsNotExist(err) {
		t.Fatalf("claim not removed: %v", err)
	}
}

func TestWatcherRefusalIsRespondedAndReported(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
		return errors.New("scheme not\nallowed")
	}}
	writeRequest(t, spoolDirectory, "r1", "file:///etc/passwd")

	report := pollOnce(t, watcher, homeVolume)

	if got := readResponse(t, spoolDirectory, "r1"); got != "refused\tscheme not allowed\n" {
		t.Fatalf("response = %q", got)
	}
	if len(report.Refusals) != 1 || report.Refusals[0].Target != "file:///etc/passwd" {
		t.Fatalf("report = %+v", report)
	}
}

func TestWatcherDropsStaleRequestsWithoutOpening(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
		t.Fatal("stale request must not be opened")
		return nil
	}}
	writeRequest(t, spoolDirectory, "old", "https://example.com")
	past := time.Now().Add(-staleRequestAge - time.Second)
	if err := os.Chtimes(filepath.Join(spoolDirectory, "old"+requestSuffix), past, past); err != nil {
		t.Fatal(err)
	}

	pollOnce(t, watcher, homeVolume)

	if entries, _ := os.ReadDir(spoolDirectory); len(entries) != 0 {
		t.Fatalf("spool not cleaned: %v", entries)
	}
}

func TestWatcherSweepsLeftoversButKeepsFreshResponses(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error { return nil }}
	oldResponse := filepath.Join(spoolDirectory, "a"+responseSuffix)
	freshResponse := filepath.Join(spoolDirectory, "b"+responseSuffix)
	unrelated := filepath.Join(spoolDirectory, "notes.txt")
	for _, file := range []string{oldResponse, freshResponse, unrelated} {
		if err := os.WriteFile(file, []byte("ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-staleLeftoverAge - time.Second)
	for _, file := range []string{oldResponse, unrelated} {
		if err := os.Chtimes(file, past, past); err != nil {
			t.Fatal(err)
		}
	}

	pollOnce(t, watcher, homeVolume)

	if _, err := os.Stat(oldResponse); !os.IsNotExist(err) {
		t.Fatal("old response not swept")
	}
	for _, file := range []string{freshResponse, unrelated} {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("%s removed: %v", file, err)
		}
	}
}

// TestWatcherServesConcurrentlyWithinBound covers a burst: requests are served
// at the same time, so a slow host handler does not delay later answers past
// the shim's timeout, but never more than maxRequestsInFlight at once.
func TestWatcherServesConcurrentlyWithinBound(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	release := make(chan struct{})
	started := make(chan struct{}, maxRequestsInFlight+3)
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
		started <- struct{}{}
		<-release
		return nil
	}}
	for index := range maxRequestsInFlight + 3 {
		writeRequest(t, spoolDirectory, "req"+string(rune('a'+index)), "https://example.com")
	}
	spool, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.close()

	watcher.poll(context.Background(), spool)
	for range maxRequestsInFlight {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("requests were not served concurrently")
		}
	}
	select {
	case <-started:
		t.Fatal("more than maxRequestsInFlight requests in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	watcher.serving.Wait()

	watcher.poll(context.Background(), spool)
	watcher.serving.Wait()
	if len(started) != 3 {
		t.Fatalf("remaining requests served %d, want 3", len(started))
	}
}

func TestWatcherRateLimitsOpens(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	handled := 0
	now := time.Now()
	watcher := &Watcher{
		HomeVolumePath: homeVolume,
		now:            func() time.Time { return now },
		Handle: func(context.Context, string) error {
			handled++
			return nil
		},
	}
	for range maxOpensPerMinute {
		watcher.recentOpens = append(watcher.recentOpens, now.Add(-59*time.Second))
	}
	writeRequest(t, spoolDirectory, "limited", "https://example.com")

	report := pollOnce(t, watcher, homeVolume)

	if handled != 0 || len(report.Refusals) != 1 ||
		!strings.Contains(readResponse(t, spoolDirectory, "limited"), "too many") {
		t.Fatalf("handled %d, report %+v", handled, report)
	}
	now = now.Add(2 * time.Second)
	writeRequest(t, spoolDirectory, "later", "https://example.com")
	pollOnce(t, watcher, homeVolume)
	if handled != 1 {
		t.Fatalf("opens did not recover after a minute, handled %d", handled)
	}
}

func TestWatcherIgnoresMalformedNames(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
		t.Fatal("malformed name must not be handled")
		return nil
	}}
	writeRequest(t, spoolDirectory, "bad.name", "https://example.com")
	writeRequest(t, spoolDirectory, ".tmp-x", "https://example.com")

	pollOnce(t, watcher, homeVolume)
}

func TestWatcherRefusesNonRegularRequest(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
		t.Fatal("directory request must not be handled")
		return nil
	}}
	if err := os.Mkdir(filepath.Join(spoolDirectory, "dir"+requestSuffix), 0o755); err != nil {
		t.Fatal(err)
	}

	report := pollOnce(t, watcher, homeVolume)

	if len(report.Refusals) != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestWatcherDoesNotFollowSymlinksOutOfTheVolume(t *testing.T) {
	homeVolume := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(outside, "x"+requestSuffix),
		[]byte("https://example.com"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(homeVolume, SpoolRelativePath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(homeVolume, SpoolRelativePath)); err != nil {
		t.Fatal(err)
	}
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
		t.Fatal("request outside the volume must not be handled")
		return nil
	}}

	pollOnce(t, watcher, homeVolume)

	if _, err := os.Stat(filepath.Join(outside, "x"+requestSuffix)); err != nil {
		t.Fatalf("file outside the volume was touched: %v", err)
	}
}

func TestWatcherReplacesPlantedResponseSymlink(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(spoolDirectory, "s"+responseSuffix)); err != nil {
		t.Fatal(err)
	}
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error { return nil }}
	writeRequest(t, spoolDirectory, "s", "https://example.com")

	pollOnce(t, watcher, homeVolume)

	if content, _ := os.ReadFile(victim); string(content) != "precious" {
		t.Fatalf("victim overwritten: %q", content)
	}
	if got := readResponse(t, spoolDirectory, "s"); got != "ok\n" {
		t.Fatalf("response = %q", got)
	}
}

func TestWatchServesUntilCancelled(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	opened := make(chan string, 1)
	watcher := &Watcher{
		HomeVolumePath: homeVolume,
		PollInterval:   5 * time.Millisecond,
		Handle: func(_ context.Context, target string) error {
			opened <- target
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := watcher.Watch(ctx)
		done <- err
	}()
	writeRequest(t, spoolDirectory, "live", "https://example.com")
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("request not served")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Watch: %v", err)
	}
}

func TestWatchFailsForMissingVolume(t *testing.T) {
	watcher := &Watcher{
		HomeVolumePath: filepath.Join(t.TempDir(), "missing"),
		Handle:         func(context.Context, string) error { return nil },
	}
	if _, err := watcher.Watch(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestReportBoundsAndSanitizes(t *testing.T) {
	var report Report
	report.addRefusal("https://x\x1b[31m/"+strings.Repeat("a", 300), "reason")
	if strings.ContainsRune(report.Refusals[0].Target, 0x1b) {
		t.Fatal("escape sequence not sanitized")
	}
	if !strings.HasSuffix(report.Refusals[0].Target, "…") {
		t.Fatal("long target not shortened")
	}
	for range maxReportedRefusals + 2 {
		report.addRefusal("t", "r")
	}
	if len(report.Refusals) != maxReportedRefusals || report.Omitted != 3 {
		t.Fatalf("refusals %d omitted %d", len(report.Refusals), report.Omitted)
	}
}

func TestHostHandler(t *testing.T) {
	var opened []string
	handler := NewHostHandler(func(target string) error {
		opened = append(opened, target)
		return nil
	}, nil)
	if err := handler(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("allowed: %v", err)
	}
	if err := handler(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("file: must be refused")
	}
	err := handler(
		context.Background(),
		"https://claude.ai/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A43117%2Fcallback",
	)
	if !errors.Is(err, errLoopbackRedirect) {
		t.Fatalf("loopback redirect: %v", err)
	}
	if len(opened) != 1 {
		t.Fatalf("opened = %q", opened)
	}
}

func TestWatcherToleratesUnreadableSpool(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		layout func(t *testing.T, homeVolume string)
	}{
		{"spool parent is a file", func(t *testing.T, homeVolume string) {
			if err := os.WriteFile(filepath.Join(homeVolume, ".local"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"spool is a file", func(t *testing.T, homeVolume string) {
			spoolPath := filepath.Join(homeVolume, SpoolRelativePath)
			if err := os.MkdirAll(filepath.Dir(spoolPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(spoolPath, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			homeVolume := t.TempDir()
			testCase.layout(t, homeVolume)
			watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
				t.Fatal("nothing to handle")
				return nil
			}}
			if report := pollOnce(t, watcher, homeVolume); len(report.Refusals) != 0 {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestWatcherRefusesSymlinkedRequest(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	if err := os.WriteFile(filepath.Join(homeVolume, "target.txt"), []byte("https://example.com"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(homeVolume, "target.txt"),
		filepath.Join(spoolDirectory, "link"+requestSuffix),
	); err != nil {
		t.Fatal(err)
	}
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
		t.Fatal("symlinked request must not be handled")
		return nil
	}}

	if report := pollOnce(t, watcher, homeVolume); len(report.Refusals) != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestWatcherSkipsRequestClaimedElsewhere(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	spool, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.close()
	watcher := &Watcher{HomeVolumePath: homeVolume, Handle: func(context.Context, string) error {
		t.Fatal("a request claimed by another watcher must not be handled")
		return nil
	}}
	watcher.initialize()

	watcher.startServing(context.Background(), spool, "gone")
	watcher.serving.Wait()

	if len(watcher.inFlight) != 0 {
		t.Fatal("in-flight slot not released")
	}
	// A failed claim must not start serving: no response is written for a
	// request this watcher does not own.
	if entries, _ := os.ReadDir(spoolDirectory); len(entries) != 0 {
		t.Fatalf("spool = %v, want no response for a claim owned elsewhere", entries)
	}
}

func TestRespondLeavesNoTempFileWhenResponseCannotBePlaced(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	if err := os.MkdirAll(filepath.Join(spoolDirectory, "blocked"+responseSuffix, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	spool, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.close()

	spool.respond("blocked", responseOpened)

	entries, _ := os.ReadDir(spoolDirectory)
	if len(entries) != 1 {
		t.Fatalf("spool = %v, want only the blocking directory", entries)
	}
}

func TestRespondToleratesReadOnlySpool(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	spool, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.close()
	if err := os.Chmod(spoolDirectory, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(spoolDirectory, 0o755) })

	spool.respond("x", responseOpened)

	if entries, _ := os.ReadDir(spoolDirectory); len(entries) != 0 {
		t.Fatalf("spool = %v", entries)
	}
}

func TestWatchUsesDefaultPollIntervalAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	watcher := &Watcher{HomeVolumePath: t.TempDir(), Handle: func(context.Context, string) error { return nil }}
	if _, err := watcher.Watch(ctx); err != nil {
		t.Fatalf("Watch: %v", err)
	}
}

func TestWaitForTick(t *testing.T) {
	tick := make(chan time.Time, 1)
	tick <- time.Now()
	if !waitForTick(t.Context(), tick) {
		t.Fatal("tick must continue polling")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if waitForTick(ctx, make(chan time.Time)) {
		t.Fatal("cancelled context must stop polling")
	}
}

func TestEffectivePollInterval(t *testing.T) {
	if got := effectivePollInterval(time.Nanosecond); got != time.Nanosecond {
		t.Fatalf("effectivePollInterval(1ns) = %v, want it kept", got)
	}
	if got := effectivePollInterval(0); got != defaultPollInterval {
		t.Fatalf("effectivePollInterval(0) = %v, want the default", got)
	}
	if got := effectivePollInterval(-time.Second); got != defaultPollInterval {
		t.Fatalf("effectivePollInterval(-1s) = %v, want the default", got)
	}
}

// TestWatchKeepsPollingAfterATick covers a request written after the first poll:
// Watch must keep serving until the context ends, not stop at the first tick.
func TestWatchKeepsPollingAfterATick(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	opened := make(chan string, 1)
	watcher := &Watcher{
		HomeVolumePath: homeVolume,
		PollInterval:   5 * time.Millisecond,
		Handle: func(_ context.Context, target string) error {
			opened <- target
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = watcher.Watch(ctx)
	}()

	time.Sleep(40 * time.Millisecond)
	writeRequest(t, spoolDirectory, "late", "https://example.com")

	select {
	case <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("a request written after the first tick was not served")
	}
	cancel()
	<-done
}

// TestWatchWaitsForInFlightHandlersAndReturnsTheReport covers shutdown: Watch
// must let an in-flight handler finish and return the report it accumulated.
func TestWatchWaitsForInFlightHandlersAndReturnsTheReport(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	started := make(chan struct{})
	release := make(chan struct{})
	watcher := &Watcher{
		HomeVolumePath: homeVolume,
		PollInterval:   5 * time.Millisecond,
		Handle: func(context.Context, string) error {
			close(started)
			<-release
			return errors.New("refused")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Report)
	go func() {
		report, _ := watcher.Watch(ctx)
		done <- report
	}()
	writeRequest(t, spoolDirectory, "r1", "file:///etc/passwd")

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("Watch returned before the in-flight handler finished")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case report := <-done:
		if len(report.Refusals) != 1 || !strings.Contains(report.Refusals[0].Reason, "refused") {
			t.Fatalf("report = %+v, want the in-flight refusal", report)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return after the handler finished")
	}
}

// TestPollKeepsAResponseOlderThanTheRequestAge covers the suffix check: only a
// request is swept at staleRequestAge; a response waits for staleLeftoverAge.
func TestPollKeepsAResponseOlderThanTheRequestAge(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	base := time.Unix(1_700_000_000, 0)
	watcher := &Watcher{
		HomeVolumePath: homeVolume,
		now:            func() time.Time { return base.Add(20 * time.Second) },
		Handle: func(context.Context, string) error {
			t.Fatal("a response must not be handled")
			return nil
		},
	}
	response := filepath.Join(spoolDirectory, "a"+responseSuffix)
	if err := os.WriteFile(response, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(response, base, base); err != nil {
		t.Fatal(err)
	}

	pollOnce(t, watcher, homeVolume)

	if _, err := os.Stat(response); err != nil {
		t.Fatalf("a response younger than staleLeftoverAge was swept: %v", err)
	}
}

// TestPollServesARequestExactlyAtTheRequestAge pins the staleRequestAge
// boundary: the sweep is strictly older-than.
func TestPollServesARequestExactlyAtTheRequestAge(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	base := time.Unix(1_700_000_000, 0)
	served := false
	watcher := &Watcher{
		HomeVolumePath: homeVolume,
		now:            func() time.Time { return base.Add(staleRequestAge) },
		Handle: func(context.Context, string) error {
			served = true
			return nil
		},
	}
	writeRequest(t, spoolDirectory, "boundary", "https://example.com")
	if err := os.Chtimes(filepath.Join(spoolDirectory, "boundary"+requestSuffix), base, base); err != nil {
		t.Fatal(err)
	}

	pollOnce(t, watcher, homeVolume)

	if !served {
		t.Fatal("a request exactly at staleRequestAge must still be served")
	}
}

// TestPollKeepsALeftoverExactlyAtTheLeftoverAge pins the staleLeftoverAge
// boundary.
func TestPollKeepsALeftoverExactlyAtTheLeftoverAge(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	base := time.Unix(1_700_000_000, 0)
	watcher := &Watcher{
		HomeVolumePath: homeVolume,
		now:            func() time.Time { return base.Add(staleLeftoverAge) },
		Handle: func(context.Context, string) error {
			t.Fatal("a leftover must not be handled")
			return nil
		},
	}
	leftover := filepath.Join(spoolDirectory, "a"+responseSuffix)
	if err := os.WriteFile(leftover, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(leftover, base, base); err != nil {
		t.Fatal(err)
	}

	pollOnce(t, watcher, homeVolume)

	if _, err := os.Stat(leftover); err != nil {
		t.Fatalf("a leftover exactly at staleLeftoverAge was swept: %v", err)
	}
}

// TestReserveOpenBoundsLiterally pins the per-minute rate bound with literals.
func TestReserveOpenBoundsLiterally(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	watcher := &Watcher{now: func() time.Time { return fixed }}
	for index := range 20 {
		if !watcher.reserveOpen() {
			t.Fatalf("open %d rejected below the bound", index+1)
		}
	}
	if watcher.reserveOpen() {
		t.Fatal("the 21st open within a minute must be rejected")
	}
}

// TestReserveOpenPrunesOpensExactlyAMinuteOld pins the recency boundary.
func TestReserveOpenPrunesOpensExactlyAMinuteOld(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	watcher := &Watcher{now: func() time.Time { return base.Add(time.Minute) }}
	watcher.recentOpens = make([]time.Time, 20)
	for index := range watcher.recentOpens {
		watcher.recentOpens[index] = base
	}
	if !watcher.reserveOpen() {
		t.Fatal("an open exactly a minute old must not count as recent")
	}
}

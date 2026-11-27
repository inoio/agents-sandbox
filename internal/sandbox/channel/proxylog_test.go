package channel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyLogWritesScrubbedLinesAndCountsThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	var failed []string
	var sessions []uint16
	log := &proxyLog{file: file, onHandshakeFailure: func(session uint16, target string) bool {
		sessions = append(sessions, session)
		failed = append(failed, target)
		return true
	}}

	log.Printf("[%03d] WARN: Cannot read response from mitm'd server %v\n", int64(7),
		errors.New(`Get "https://api.example/v1?key=query-secret": EOF`))
	log.Printf("[%03d] WARN: Cannot handshake client %v %v\n", int64(8), "160.79.104.10:443",
		errors.New("remote error: tls: bad certificate; token=error-secret"))
	result := log.close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"query-secret", "error-secret", "bad certificate", "160.79.104.10"} {
		if strings.Contains(string(content), secret) {
			t.Errorf("log = %q, contains sensitive or untrusted argument %q", content, secret)
		}
	}
	if !strings.Contains(string(content), "TLS handshake failed") ||
		!strings.Contains(string(content), "upstream response") {
		t.Errorf("log = %q, want the known warning categories", content)
	}
	if result.Warnings != 2 {
		t.Errorf("Warnings = %d, want 2", result.Warnings)
	}
	if result.UnattributedHandshakes != 0 {
		t.Errorf("UnattributedHandshakes = %d, want only handshake warnings attributed", result.UnattributedHandshakes)
	}
	if len(failed) != 1 || failed[0] != "160.79.104.10:443" || len(sessions) != 1 || sessions[0] != 8 {
		t.Errorf("handshake failures = sessions %v targets %v, want session 8 and its CONNECT target", sessions, failed)
	}
}

// TestProxyLogOmitsUntrustedHeaderArguments pins that a goproxy warning whose
// error quotes a malformed credential header persists only its safe category.
func TestProxyLogOmitsUntrustedHeaderArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file}

	log.Printf("[%03d] WARN: Cannot read request from mitm'd client %v %v\n", 3, "api.example:443",
		errors.New(`malformed MIME header line: "Authorization: Bearer header-secret\x00"`))
	result := log.close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "header-secret") || strings.Contains(string(content), "Bearer") {
		t.Errorf("log = %q, want the quoted header line omitted", content)
	}
	if !strings.Contains(string(content), "intercepted client request") {
		t.Errorf("log = %q, want the safe warning category", content)
	}
	if result.UnattributedHandshakes != 0 {
		t.Errorf("close = %+v, want the request warning not treated as a handshake", result)
	}
}

// TestProxyLogOpensOnTheFirstWarningOnly: a clean session creates no file, and
// a session that warns opens its log once.
func TestProxyLogOpensOnTheFirstWarningOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	opened := 0
	log := &proxyLog{openLog: func() (*os.File, error) {
		opened++
		return os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	}}
	if result := (&proxyLog{openLog: log.openLog}).close(); result.Path != "" || result.Warnings != 0 || opened != 0 {
		t.Fatalf("a clean session = %+v, opened %d times, want no file", result, opened)
	}

	log.Printf("[%03d] WARN: one\n", 1)
	log.Printf("[%03d] WARN: two\n", 2)
	result := log.close()

	if opened != 1 || result.Path != path || result.Warnings != 2 || result.WriteFailed {
		t.Errorf("close = %+v, opened %d times, want %q with 2 warnings opened once", result, opened, path)
	}
}

// TestProxyLogOmitsURLArguments pins that an unparsable URL and its error are
// not formatted into the diagnostic log.
func TestProxyLogOmitsURLArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file}

	log.Printf(
		"[%03d] WARN: Cannot parse URL %s: %v\n",
		int64(1),
		"https://user:password-secret@example.com:abc/p?token=query-secret",
		errors.New(
			`parse "https://user:password-secret@example.com:abc/p?token=query-secret": invalid port ":abc" after host`,
		),
	)
	result := log.close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user", "password-secret", "query-secret", "example.com"} {
		if strings.Contains(string(content), secret) {
			t.Errorf("log = %q, contains URL argument %q", content, secret)
		}
	}
	if !strings.Contains(string(content), "request URL") {
		t.Errorf("log = %q, want the safe parse-warning category", content)
	}
	if result.UnattributedHandshakes != 0 {
		t.Errorf("close = %+v, want the URL warning not treated as a handshake", result)
	}
}

func TestProxyLogDropsLinesAfterClose(t *testing.T) {
	log := &proxyLog{}
	log.Printf("[%03d] WARN: before\n", 1)
	log.close()

	log.Printf("[%03d] WARN: after\n", 2)
	log.Printf("[%03d] WARN: after again\n", 3)

	if result := log.close(); result.Warnings != 1 {
		t.Errorf("close = %+v, want only the warning before close", result)
	}
}

func TestProxyLogDoesNotOpenAfterClose(t *testing.T) {
	opened := 0
	var openedFile *os.File
	log := &proxyLog{openLog: func() (*os.File, error) {
		opened++
		var err error
		openedFile, err = os.CreateTemp(t.TempDir(), "proxy.log")
		return openedFile, err
	}}
	log.close()
	log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(1), "https://example.com", errors.New("bad URL"))
	if openedFile != nil {
		_ = openedFile.Close()
	}
	if opened != 0 {
		t.Errorf("openLog calls = %d, want no file opened after close", opened)
	}
}

func TestProxyLogReportsAHandshakeWarningWithItsTargetOnly(t *testing.T) {
	var sessions []uint16
	var targets []string
	log := &proxyLog{onHandshakeFailure: func(session uint16, target string) bool {
		sessions = append(sessions, session)
		targets = append(targets, target)
		return true
	}}

	log.Printf("[%03d] WARN: Cannot handshake client %v %v\n", int64(1), "203.0.113.7:443", errors.New("EOF"))

	if len(targets) != 1 || targets[0] != "203.0.113.7:443" || len(sessions) != 1 || sessions[0] != 1 {
		t.Errorf("handshake = sessions %v targets %v, want the session and target", sessions, targets)
	}
	if result := log.close(); result.Path != "" || result.Warnings != 1 || result.UnattributedHandshakes != 0 {
		t.Errorf("close = %+v, want no file and one attributed warning", result)
	}
}

func TestProxyLogClosesAfterTheHandshakeCallbackCompletes(t *testing.T) {
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	log := &proxyLog{onHandshakeFailure: func(uint16, string) bool {
		close(callbackStarted)
		<-releaseCallback
		return true
	}}
	printfDone := make(chan struct{})
	go func() {
		log.Printf(handshakeFailureWarning, int64(4), "api.example:443", errors.New("EOF"))
		close(printfDone)
	}()
	<-callbackStarted

	closeDone := make(chan proxyLogResult, 1)
	go func() { closeDone <- log.close() }()
	if log.mu.TryLock() {
		log.mu.Unlock()
		t.Fatal("handshake callback does not hold the logger lock")
	}
	close(releaseCallback)
	<-printfDone
	result := <-closeDone
	if result.Warnings != 1 || result.UnattributedHandshakes != 0 {
		t.Errorf("close = %+v, want the completed handshake callback in the snapshot", result)
	}
}

func TestProxyLogCountsHandshakeWithoutCallbackAsUnattributed(t *testing.T) {
	log := &proxyLog{}
	log.Printf("[%03d] WARN: Cannot handshake client %v %v\n", 1, "203.0.113.7:443", "EOF")
	if result := log.close(); result.UnattributedHandshakes != 1 || result.WriteFailed {
		t.Errorf("close = %+v, want the missing callback to count an unattributed handshake", result)
	}
}

func TestProxyLogRequiresTheHandshakeErrorArgument(t *testing.T) {
	called := false
	log := &proxyLog{onHandshakeFailure: func(uint16, string) bool { called = true; return true }}
	log.Printf(handshakeFailureWarning, int64(1), "api.example:443")
	result := log.close()
	if called || result.UnattributedHandshakes != 1 {
		t.Errorf("close = %+v callback-called=%v, want missing-error warning left unattributed", result, called)
	}
}

func TestProxyLogCountsAShortHandshakeWarningAsUnattributed(t *testing.T) {
	called := false
	log := &proxyLog{onHandshakeFailure: func(uint16, string) bool { called = true; return true }}

	log.Printf("[%03d] WARN: Cannot handshake client\n", 1)

	if called {
		t.Error("a handshake warning without its target reported a failure")
	}
	if result := log.close(); result.Warnings != 1 || result.UnattributedHandshakes != 1 {
		t.Errorf("close = %+v, want the warning counted and failure left unattributed", result)
	}
}

func TestProxyLogUsesAGenericSafeLineForUnknownWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file}

	log.Printf("[%03d] WARN: unrecognized warning with embedded-secret\n", int64(3))
	result := log.close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "embedded-secret") || !strings.Contains(string(content), "details omitted") {
		t.Errorf("log = %q, want unknown warning details omitted", content)
	}
	if result.UnattributedHandshakes != 0 {
		t.Errorf("close = %+v, want the unknown warning not treated as a handshake", result)
	}
}

func TestProxyLogUsesSafeDialErrorCategory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file}
	log.Printf("[%03d] WARN: Error dialing to %s: %s\n", int64(7), "api.example:443", "token=secret-value")
	result := log.close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "could not dial the upstream destination") ||
		strings.Contains(string(content), "api.example") || strings.Contains(string(content), "secret-value") {
		t.Errorf("log = %q, want the safe dial category with arguments omitted", content)
	}
	if result.UnattributedHandshakes != 0 {
		t.Errorf("close = %+v, want the dial warning not treated as a handshake", result)
	}
}

func TestProxyLogCountsMalformedHandshakeArgumentsAsUnattributed(t *testing.T) {
	cases := map[string][]any{
		"bad session type": {"session", "api.example:443", errors.New("EOF")},
		"missing target":   {int64(1), "", errors.New("EOF")},
		"bad target type":  {int64(1), 443, errors.New("EOF")},
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			log := &proxyLog{}
			log.Printf(handshakeFailureWarning, arguments...)
			result := log.close()
			if result.Warnings != 1 || result.UnattributedHandshakes != 1 {
				t.Errorf("close = %+v, want malformed handshake counted as unattributed", result)
			}
		})
	}
}

func TestProxyLogClosesAFileReturnedWithAnOpenError(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "proxy.log")
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{openLog: func() (*os.File, error) { return file, errors.New("open failed") }}
	log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(1), "https://example.com", errors.New("bad URL"))
	result := log.close()

	if !result.WriteFailed || result.Path != "" {
		t.Errorf("close = %+v, want an open failure and no persisted path", result)
	}
	if _, err := file.Stat(); err == nil {
		t.Error("proxy log kept a file that arrived with an open error")
	}
}

func TestProxyLogBoundsItsOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file}
	line, _ := safeProxyWarning("unknown format")
	for range 10 {
		log.writeLineWithinLimit(line, 80)
	}
	result := log.close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) > 80 || !result.Truncated {
		t.Errorf(
			"content bytes=%d result=%+v, want <=80 bytes and truncated",
			len(content),
			result,
		)
	}
}

func TestProxyLogKeepsLineTruncationStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file}
	log.writeLine(strings.Repeat("x", 513))
	log.writeLine("ok\n")

	if result := log.close(); !result.Truncated {
		t.Errorf("close = %+v, want the earlier oversized line to remain reported as truncated", result)
	}
}

func TestProxyLogLineLimitAtBoundary(t *testing.T) {
	for _, tc := range []struct {
		inputLength   int
		wantLength    int
		wantTruncated bool
	}{
		{inputLength: 512, wantLength: 512},
		{inputLength: 513, wantLength: 512, wantTruncated: true},
	} {
		line, truncated := truncateProxyLogLine(strings.Repeat("x", tc.inputLength))
		if len(line) != tc.wantLength || truncated != tc.wantTruncated {
			t.Errorf("input=%d got length=%d truncated=%v, want length=%d truncated=%v",
				tc.inputLength, len(line), truncated, tc.wantLength, tc.wantTruncated)
		}
		if truncated && !strings.HasSuffix(line, "…\n") {
			t.Errorf("truncated line suffix = %q, want marker", line[len(line)-4:])
		}
	}
}

func TestProxyLogByteLimitIsOneMiB(t *testing.T) {
	if maxProxyLogBytes != 1<<20 {
		t.Errorf("maxProxyLogBytes = %d, want 1 MiB", maxProxyLogBytes)
	}
	for _, tc := range []struct {
		name         string
		bytesWritten int
		lineBytes    int
		limit        int
		wantSpace    bool
	}{
		{name: "exact fit", bytesWritten: 64, lineBytes: 16, limit: 80, wantSpace: true},
		{name: "one byte over", bytesWritten: 64, lineBytes: 17, limit: 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyLogHasSpace(tc.bytesWritten, tc.lineBytes, tc.limit); got != tc.wantSpace {
				t.Errorf("proxyLogHasSpace = %v, want %v", got, tc.wantSpace)
			}
		})
	}
}

//nolint:nilnil // This test exercises an opener that violates its file-or-error contract.
func TestProxyLogReportsOpenAndWriteFailures(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		log := &proxyLog{openLog: func() (*os.File, error) { return nil, errors.New("open failed") }}
		log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(1), "https://example.com", errors.New("bad URL"))
		result := log.close()
		if !result.WriteFailed || result.Path != "" || result.Warnings != 1 {
			t.Errorf("close = %+v, want a failed write with one warning and no path", result)
		}
	})

	t.Run("nil file without error", func(t *testing.T) {
		log := &proxyLog{openLog: func() (*os.File, error) { return nil, nil }}
		log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(1), "https://example.com", errors.New("bad URL"))
		result := log.close()
		if !result.WriteFailed || result.Path != "" || result.Warnings != 1 {
			t.Errorf("close = %+v, want the nil file reported as a log-write failure", result)
		}
	})

	t.Run("write", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "proxy.log")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		log := &proxyLog{file: file}
		log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(1), "https://example.com", errors.New("bad URL"))
		result := log.close()
		if !result.WriteFailed || result.Path != path || result.Warnings != 1 {
			t.Errorf("close = %+v, want a failed write with its path and one warning", result)
		}
	})
}

func TestProxyLogReportsAnErrorAfterWritingTheFullLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	orig := writeProxyLog
	writeProxyLog = func(_ *os.File, line string) (int, error) {
		return len(line), errors.New("write completion error")
	}
	t.Cleanup(func() { writeProxyLog = orig })
	log := &proxyLog{file: file}

	log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(1), "https://example.com", errors.New("bad URL"))
	result := log.close()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !result.WriteFailed || len(content) != 0 {
		t.Errorf("close = %+v file bytes=%d, want the injected write error reported", result, len(content))
	}
}

func TestProxyLogReportsCloseFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	orig := closeProxyLog
	closeProxyLog = func(*os.File) error { return errors.New("close failed") }
	t.Cleanup(func() {
		closeProxyLog = orig
		_ = file.Close()
	})
	log := &proxyLog{file: file}
	log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(1), "https://example.com", errors.New("bad URL"))

	result := log.close()
	if !result.WriteFailed || result.Path != path {
		t.Errorf("close = %+v, want the close error with the persisted path", result)
	}
}

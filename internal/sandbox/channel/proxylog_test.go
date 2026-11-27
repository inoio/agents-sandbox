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
	log.close()

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
	log.close()

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
	if result := log.close(); result.UnattributedHandshakes != 1 {
		t.Errorf("close = %+v, want the missing callback to count an unattributed handshake", result)
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
	log.close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "embedded-secret") || !strings.Contains(string(content), "details omitted") {
		t.Errorf("log = %q, want unknown warning details omitted", content)
	}
}

func TestProxyLogBoundsItsOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file, maxBytes: 80}
	for index := range 10 {
		log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(index), "https://example.com", errors.New("bad URL"))
	}
	result := log.close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) > 80 || !result.Truncated || result.Warnings != 10 {
		t.Errorf(
			"content bytes=%d result=%+v, want <=80 bytes, truncated, and all 10 warnings counted",
			len(content),
			result,
		)
	}
}

func TestProxyLogTruncatesAnOversizedLine(t *testing.T) {
	line, truncated := truncateProxyLogLine(strings.Repeat("x", maxProxyLogLineBytes+1))
	if len(line) > maxProxyLogLineBytes || !truncated || !strings.HasSuffix(line, "…\n") {
		t.Errorf("truncated line length=%d truncated=%v, want the size cap and marker", len(line), truncated)
	}
}

func TestProxyLogReportsOpenAndWriteFailures(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		log := &proxyLog{openLog: func() (*os.File, error) { return nil, errors.New("open failed") }}
		log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", int64(1), "https://example.com", errors.New("bad URL"))
		result := log.close()
		if !result.WriteFailed || result.Path != "" || result.Warnings != 1 {
			t.Errorf("close = %+v, want a failed write with one warning and no path", result)
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

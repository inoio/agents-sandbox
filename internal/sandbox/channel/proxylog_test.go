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
	log := &proxyLog{file: file, onHandshakeFailure: func(target string) { failed = append(failed, target) }}

	log.Printf("[%03d] WARN: Cannot read response from mitm'd server %v\n", 7,
		errors.New(`Get "https://api.example/v1?key=sk-secret&x=1": EOF`))
	log.Printf("[%03d] WARN: Cannot handshake client %v %v\n", 8, "160.79.104.10:443",
		errors.New("remote error: tls: bad certificate"))
	_ = file.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "sk-secret") {
		t.Errorf("log = %q, want the query string scrubbed", content)
	}
	if !strings.Contains(string(content), `Get "…": EOF`) {
		t.Errorf("log = %q, want the quoted URL redacted and the error kept", content)
	}
	if log.lines != 2 {
		t.Errorf("lines = %d, want 2", log.lines)
	}
	if len(failed) != 1 || failed[0] != "160.79.104.10:443" {
		t.Errorf("handshake failures = %v, want the CONNECT target", failed)
	}
}

// TestProxyLogRedactsQuotedValues: net/textproto quotes a malformed header line
// whole in its error, and goproxy logs that error, so a credential-bearing
// header could reach the log. Every quoted segment is redacted.
func TestProxyLogRedactsQuotedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file}

	log.Printf("[%03d] WARN: Cannot read request from mitm'd client %v %v\n", 3, "api.example:443",
		errors.New(`malformed MIME header line: "Authorization: Bearer sk-secret\x00"`))
	_ = file.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "sk-secret") || strings.Contains(string(content), "Bearer") {
		t.Errorf("log = %q, want the quoted header line redacted", content)
	}
	if !strings.Contains(string(content), "malformed MIME header line") {
		t.Errorf("log = %q, want the error's kind kept", content)
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
	if logPath, lines := (&proxyLog{openLog: log.openLog}).close(); logPath != "" || lines != 0 || opened != 0 {
		t.Fatalf("a clean session = (%q, %d), opened %d times, want no file", logPath, lines, opened)
	}

	log.Printf("[%03d] WARN: one\n", 1)
	log.Printf("[%03d] WARN: two\n", 2)
	logPath, lines := log.close()

	if opened != 1 || logPath != path || lines != 2 {
		t.Errorf("close = (%q, %d), opened %d times, want %q with 2 lines opened once", logPath, lines, opened, path)
	}
}

// TestProxyLogStripsAnUnquotedQuery: goproxy prints the URL it cannot parse
// unquoted (%s), so its query string is stripped on its own.
func TestProxyLogStripsAnUnquotedQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log := &proxyLog{file: file}

	log.Printf("[%03d] WARN: Cannot parse URL %s: %v\n", 1, "https://example.com:abc/p?token=SECRET",
		errors.New(`parse "https://example.com:abc/p?token=SECRET": invalid port ":abc" after host`))
	_ = file.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "SECRET") {
		t.Errorf("log = %q, want the query stripped", content)
	}
}

func TestProxyLogDropsLinesAfterClose(t *testing.T) {
	log := &proxyLog{}
	log.Printf("[%03d] WARN: before\n", 1)
	log.close()

	log.Printf("[%03d] WARN: after\n", 2)
	log.Printf("[%03d] WARN: after again\n", 3)

	if _, lines := log.close(); lines != 1 {
		t.Errorf("lines = %d, want only the line before close", lines)
	}
}

func TestProxyLogReportsAHandshakeWarningWithItsTargetOnly(t *testing.T) {
	var targets []string
	log := &proxyLog{onHandshakeFailure: func(target string) { targets = append(targets, target) }}

	log.Printf("[%03d] WARN: Cannot handshake client %v\n", 1, "203.0.113.7:443")

	if len(targets) != 1 || targets[0] != "203.0.113.7:443" {
		t.Errorf("targets = %v, want the target of a two-argument warning", targets)
	}
	if path, lines := log.close(); path != "" || lines != 1 {
		t.Errorf("close = (%q, %d), want no file and one line", path, lines)
	}
}

func TestProxyLogWithoutACallbackIgnoresHandshakeWarnings(t *testing.T) {
	log := &proxyLog{}
	log.Printf("[%03d] WARN: Cannot handshake client %v %v\n", 1, "203.0.113.7:443", "EOF")
}

func TestProxyLogIgnoresAShortHandshakeWarning(t *testing.T) {
	called := false
	log := &proxyLog{onHandshakeFailure: func(string) { called = true }}

	log.Printf("[%03d] WARN: Cannot handshake client\n", 1)

	if called {
		t.Error("a handshake warning without its target reported a failure")
	}
	if log.lines != 1 {
		t.Errorf("lines = %d, want the line counted even without a file", log.lines)
	}
}

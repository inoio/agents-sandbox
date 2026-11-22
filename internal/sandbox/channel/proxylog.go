package channel

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

// handshakeFailureWarning is goproxy's warning for a client that aborted the
// terminating TLS handshake; its first argument after the session number is
// the CONNECT target.
const handshakeFailureWarning = "Cannot handshake client"

// quotedValue matches a quoted segment of a logged error. Go quotes the
// payload it failed on — net/textproto a whole malformed header line, net/http
// the request URL with its query — and either can carry a credential, so every
// quoted segment is redacted before a line is written.
var quotedValue = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// urlQuery matches a query string goproxy prints unquoted ("Cannot parse URL
// https://host/p?token=..."), so a key in it never reaches the log.
var urlQuery = regexp.MustCompile(`\?[^\s"]*`)

// proxyLog is goproxy's logger for an observation session. It writes every
// warning to the session's log file (never the terminal, where it would
// interleave with the agent's TUI) with quoted payloads redacted, counts them, and reports a failed
// terminating handshake so the tunnel is not counted as reached. A nil file
// only counts.
type proxyLog struct {
	mu                 sync.Mutex
	file               *os.File
	openLog            func() (*os.File, error)
	lines              int
	closed             bool
	onHandshakeFailure func(connectTarget string)
}

// Printf implements goproxy's Logger.
func (l *proxyLog) Printf(format string, args ...any) {
	line := quotedValue.ReplaceAllString(fmt.Sprintf(format, args...), `"…"`)
	line = urlQuery.ReplaceAllString(line, "?…")
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.lines++
	if l.file == nil && l.openLog != nil {
		// Open on the first warning only, so a clean session leaves no file;
		// a failed open is not retried, and the lines are still counted.
		l.file, _ = l.openLog()
		l.openLog = nil
	}
	if l.file != nil {
		_, _ = l.file.WriteString(line)
	}
	l.mu.Unlock()
	// goproxy's printf prepends the session number, so the CONNECT target is
	// the second argument.
	if strings.Contains(format, handshakeFailureWarning) && len(args) >= 2 && l.onHandshakeFailure != nil {
		l.onHandshakeFailure(fmt.Sprint(args[1]))
	}
}

// close closes the log file and returns its path and line count. The path is
// empty when no file was written (a clean session, or an open that failed).
func (l *proxyLog) close() (string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.file == nil {
		return "", l.lines
	}
	path := l.file.Name()
	_ = l.file.Close()
	l.file = nil
	return path, l.lines
}

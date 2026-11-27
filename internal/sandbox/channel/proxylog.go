package channel

import (
	"os"
	"sync"
)

const (
	handshakeFailureWarning = "[%03d] WARN: Cannot handshake client %v %v\n"
	maxProxyLogLineBytes    = 512
	maxProxyLogBytes        = 1 << 20
)

// writeProxyLog and closeProxyLog expose the file-failure paths to unit tests.
var (
	writeProxyLog = (*os.File).WriteString
	closeProxyLog = (*os.File).Close
)

type proxyLogResult struct {
	Path                   string
	Warnings               int
	UnattributedHandshakes int
	Truncated              bool
	WriteFailed            bool
}

// proxyLog stores an allow-listed rendering of goproxy warnings. It never
// formats library arguments: those can contain request URLs, headers, or other
// credential-bearing values. Unknown warning formats keep only a generic line.
type proxyLog struct {
	mu                 sync.Mutex
	file               *os.File
	openLog            func() (*os.File, error)
	warnings           int
	unattributed       int
	bytesWritten       int
	truncated          bool
	writeFailed        bool
	closed             bool
	result             proxyLogResult
	onHandshakeFailure func(sessionID uint16, connectTarget string) bool
}

// Printf implements goproxy's Logger. It counts every warning, but writes only
// a fixed, value-free message for warning types whose format is known.
func (l *proxyLog) Printf(format string, args ...any) {
	line, handshakeFailure := safeProxyWarning(format)
	var sessionID uint16
	var connectTarget string
	gotHandshakeSession := false
	if handshakeFailure {
		sessionID, connectTarget, gotHandshakeSession = handshakeWarningTarget(args)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.warnings++
	l.recordHandshakeFailure(handshakeFailure, gotHandshakeSession, sessionID, connectTarget)
	l.openOnFirstWarning()
	l.writeLine(line)
}

// recordHandshakeFailure runs under l.mu so close cannot snapshot the warning
// before its tunnel failure is recorded.
func (l *proxyLog) recordHandshakeFailure(handshakeFailure, gotSession bool, sessionID uint16, connectTarget string) {
	if !handshakeFailure {
		return
	}
	attributed := gotSession && l.onHandshakeFailure != nil &&
		l.onHandshakeFailure(sessionID, connectTarget)
	if !attributed {
		l.unattributed++
	}
}

// openOnFirstWarning opens the file lazily, so a clean session leaves no log.
func (l *proxyLog) openOnFirstWarning() {
	if l.openLog != nil {
		openLog := l.openLog
		l.openLog = nil
		file, err := openLog()
		if err != nil || file == nil {
			l.writeFailed = true
			if file != nil {
				_ = file.Close()
			}
		} else {
			l.file = file
		}
	}
}

// writeLine keeps log storage below its per-session byte limit.
func (l *proxyLog) writeLine(line string) {
	l.writeLineWithinLimit(line, maxProxyLogBytes)
}

func (l *proxyLog) writeLineWithinLimit(line string, writeLimit int) {
	if l.file == nil {
		return
	}
	line, lineTruncated := truncateProxyLogLine(line)
	l.truncated = l.truncated || lineTruncated
	if !proxyLogHasSpace(l.bytesWritten, len(line), writeLimit) {
		l.truncated = true
		return
	}
	written, err := writeProxyLog(l.file, line)
	l.bytesWritten += written
	if err != nil || written != len(line) {
		l.writeFailed = true
	}
}

func proxyLogHasSpace(bytesWritten, lineBytes, limit int) bool {
	return lineBytes <= limit-bytesWritten
}

func safeProxyWarning(format string) (string, bool) {
	switch format {
	case handshakeFailureWarning:
		return "WARN: TLS handshake failed (target and error details redacted).\n", true
	case "[%03d] WARN: Cannot handshake client\n":
		return "WARN: TLS handshake failed (target and error details redacted).\n", true
	case "[%03d] WARN: Cannot read request from mitm'd client %v %v\n":
		return "WARN: could not read an intercepted client request (details redacted).\n", false
	case "[%03d] WARN: Cannot parse URL %s: %v\n":
		return "WARN: could not parse an intercepted request URL (details redacted).\n", false
	case "[%03d] WARN: Cannot read response from mitm'd server %v\n":
		return "WARN: could not read the upstream response (details redacted).\n", false
	case "[%03d] WARN: Error dialing to %s: %s\n":
		return "WARN: could not dial the upstream destination (details redacted).\n", false
	default:
		return "WARN: proxy warning (details omitted).\n", false
	}
}

func handshakeWarningTarget(args []any) (uint16, string, bool) {
	var noSession uint16
	if len(args) < 3 {
		return noSession, "", false
	}
	rawSession, sessionOK := args[0].(int64)
	if !sessionOK {
		return noSession, "", false
	}
	rawTarget, targetOK := args[1].(string)
	if !targetOK || rawTarget == "" {
		return noSession, "", false
	}
	return proxySessionID(rawSession), rawTarget, true
}

func truncateProxyLogLine(line string) (string, bool) {
	if len(line) <= maxProxyLogLineBytes {
		return line, false
	}
	const marker = "…\n"
	return line[:maxProxyLogLineBytes-len(marker)] + marker, true
}

// close closes the log file and returns its path and status. Repeated calls
// return the same result, so Stop remains idempotent.
func (l *proxyLog) close() proxyLogResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.result
	}
	l.closed = true
	path := ""
	if l.file != nil {
		path = l.file.Name()
		if err := closeProxyLog(l.file); err != nil {
			l.writeFailed = true
		}
		l.file = nil
	}
	l.result = proxyLogResult{
		Path:                   path,
		Warnings:               l.warnings,
		UnattributedHandshakes: l.unattributed,
		Truncated:              l.truncated,
		WriteFailed:            l.writeFailed,
	}
	return l.result
}

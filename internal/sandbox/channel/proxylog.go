package channel

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
)

const (
	handshakeFailureWarning     = "[%03d] WARN: Cannot handshake client %v %v\n"
	websocketCopyWarning        = "[%03d] WARN: Error copying to client: %s\n"
	websocketWriteHeaderWarning = "[%03d] WARN: Cannot write response header from mitm'd client: %v\n"
	websocketFlushHeaderWarning = "[%03d] WARN: Cannot flush response header from mitm'd client: %v\n"
	websocketHijackWarning      = "[%03d] WARN: Hijack error: %v\n"
	maxProxyLogLineBytes        = 512
	maxProxyLogBytes            = 1 << 20
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
	stopping           bool
	result             proxyLogResult
	websocketRelays    map[uint16]*websocketRelayGroup
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
	completesWebsocketIO := completesWebsocketIOWarning(format)
	if l.stopping && shutdownWarning(argumentsError(args)) {
		if completesWebsocketIO {
			l.completePendingWebsocketWarning(args)
		}
		return
	}
	l.warnings++
	l.recordHandshakeFailure(handshakeFailure, gotHandshakeSession, sessionID, connectTarget)
	if completesWebsocketIO {
		l.completePendingWebsocketWarning(args)
	}
	l.openOnFirstWarning()
	l.writeLine(line)
}

func completesWebsocketIOWarning(format string) bool {
	switch format {
	case websocketCopyWarning, websocketWriteHeaderWarning, websocketFlushHeaderWarning, websocketHijackWarning:
		return true
	default:
		return false
	}
}

// completePendingWebsocketWarning is called under l.mu after goproxy reports a
// tracked WebSocket I/O failure. It also runs for shutdown errors, which are
// omitted from the log but must still release the relay's join.
func (l *proxyLog) completePendingWebsocketWarning(args []any) {
	sessionID, ok := proxyWarningSessionID(args)
	if !ok {
		return
	}
	if group := l.websocketRelays[sessionID]; group != nil {
		group.completePendingWarning()
	}
}

func argumentsError(arguments []any) error {
	if len(arguments) == 0 {
		return nil
	}
	err, _ := arguments[len(arguments)-1].(error)
	return err
}

func shutdownWarning(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled)
}

func (l *proxyLog) beginShutdown() {
	l.mu.Lock()
	l.stopping = true
	l.mu.Unlock()
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

func (l *proxyLog) registerWebsocketRelay(sessionID uint16) *websocketRelayGroup {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.websocketRelays == nil {
		l.websocketRelays = make(map[uint16]*websocketRelayGroup)
	}
	group := l.websocketRelays[sessionID]
	if group == nil {
		group = newWebsocketRelayGroup()
		l.websocketRelays[sessionID] = group
	}
	group.retain()
	return group
}

func (l *proxyLog) unregisterWebsocketRelay(sessionID uint16, group *websocketRelayGroup) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.websocketRelays[sessionID] != group {
		return
	}
	group.release()
	if group.idle() {
		delete(l.websocketRelays, sessionID)
	}
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
	case websocketCopyWarning:
		return "WARN: could not relay a WebSocket frame (details redacted).\n", false
	case websocketWriteHeaderWarning, websocketFlushHeaderWarning:
		return "WARN: could not send a WebSocket upgrade response (details redacted).\n", false
	case websocketHijackWarning:
		return "WARN: could not hand off the WebSocket connection (details redacted).\n", false
	default:
		return "WARN: proxy warning (details omitted).\n", false
	}
}

func proxyWarningSessionID(args []any) (uint16, bool) {
	var noSession uint16
	if len(args) == 0 {
		return noSession, false
	}
	rawSession, ok := args[0].(int64)
	if !ok {
		return noSession, false
	}
	return proxySessionID(rawSession), true
}

func handshakeWarningTarget(args []any) (uint16, string, bool) {
	var noSession uint16
	if len(args) < 3 {
		return noSession, "", false
	}
	sessionID, ok := proxyWarningSessionID(args)
	if !ok {
		return noSession, "", false
	}
	target, targetOK := args[1].(string)
	if !targetOK || target == "" {
		return noSession, "", false
	}
	return sessionID, target, true
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

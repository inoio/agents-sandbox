package termio

import "os"

// discardTypeAhead is the platform's input-queue flush; a seam so a test can
// observe the call without a terminal.
var discardTypeAhead = discardTerminalInputQueue

// DiscardTypeAhead drops input typed before the next prompt that no reader has
// consumed: keys meant for a full-screen program that has already quit (a
// second Ctrl-D, read by the prompt as end of input) must not answer it. It
// does nothing when stdin is not a terminal, so scripted input stays intact.
func (p *printer) DiscardTypeAhead() {
	file, ok := p.stdinFile()
	if !ok || !p.isTerminal(int(file.Fd())) {
		return
	}
	// A failed flush leaves the queue as it was, which is today's behaviour.
	_ = discardTypeAhead(int(file.Fd()))
	p.stdinReader = nil
}

// stdinFile is the file behind the printer's stdin: os.Stdin by default, or the
// injected reader when it is a file.
func (p *printer) stdinFile() (*os.File, bool) {
	if p.stdin == nil {
		return os.Stdin, true
	}
	file, ok := p.stdin.(*os.File)
	return file, ok
}

package termio

import (
	"bufio"
	"fmt"
	"io"
)

const (
	ansiReset        = "\x1b[0m"
	ansiRed          = "\x1b[31m"
	ansiGreen        = "\x1b[32m"
	ansiYellow       = "\x1b[33m"
	ansiBlackIntense = "\x1b[90m"
	ansiCyanBold     = "\x1b[1;36m"
	ansiGreenBold    = "\x1b[1;32m"
	ansiYellowBold   = "\x1b[1;33m"
	ansiRedBold      = "\x1b[1;31m"
	ansiDim          = "\x1b[2m"
)

type printer struct {
	stdin       io.Reader
	stdinReader *bufio.Reader
	stdout      io.Writer
	stderr      io.Writer
	level       Level
	color       bool
	assumeYes   bool
	quiet       bool
	isTerminal  func(int) bool
}

func (p *printer) write(w io.Writer, color, msg string) {
	if p.color {
		fmt.Fprintf(w, "%s%s%s\n", color, msg, ansiReset)
		return
	}
	// With color disabled, strip any ANSI codes embedded in msg so styled cell
	// values (e.g., colored statuses) render as plain text on non-TTY output.
	fmt.Fprintln(w, stripANSICodes(msg))
}

func (p *printer) format(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func (p *printer) Info(msg string) {
	if p.level < LevelInfo {
		return
	}
	p.write(p.stderr, "", msg)
}

func (p *printer) Infof(format string, args ...any) {
	if p.level < LevelInfo {
		return
	}
	p.write(p.stderr, "", p.format(format, args...))
}

func (p *printer) Warn(msg string) {
	if p.level < LevelWarning {
		return
	}
	p.write(p.stderr, ansiYellow, msg)
}

func (p *printer) Warnf(format string, args ...any) {
	if p.level < LevelWarning {
		return
	}
	p.write(p.stderr, ansiYellow, p.format(format, args...))
}

func (p *printer) Error(msg string, err error) {
	p.write(p.stderr, ansiRed, fmt.Sprintf("%s: %v", msg, err))
}

func (p *printer) Errorf(format string, args ...any) {
	p.write(p.stderr, ansiRed, p.format(format, args...))
}

func (p *printer) Verbose(msg string) {
	if p.level < LevelVerbose {
		return
	}
	p.write(p.stderr, ansiBlackIntense, msg)
}

func (p *printer) Verbosef(format string, args ...any) {
	if p.level < LevelVerbose {
		return
	}
	p.write(p.stderr, ansiBlackIntense, p.format(format, args...))
}

func (p *printer) Out(msg string) {
	if p.quiet {
		return
	}
	p.write(p.stdout, "", msg)
}

// Header writes a table header line to stdout. When color is enabled the
// whole line is rendered bold and cyan, matching microsandbox table headers.
func (p *printer) Header(msg string) {
	if p.quiet {
		return
	}
	p.write(p.stdout, ansiCyanBold, msg)
}

// NewTable returns an empty aligned Table that prints through p.
func (p *printer) NewTable(headers ...string) *Table {
	//nolint:exhaustruct // rows starts empty (nil slice is the zero value)
	return &Table{ui: p, headers: headers}
}

func (p *printer) Outf(format string, args ...any) {
	if p.quiet {
		return
	}
	p.write(p.stdout, "", p.format(format, args...))
}

func (p *printer) SetLevel(level Level) {
	p.level = level
}

func (p *printer) SetAssumeYes(assumeYes bool) {
	p.assumeYes = assumeYes
}

func (p *printer) SetQuiet(quiet bool) {
	p.quiet = quiet
}

func (p *printer) Spinner(msg string) Spinner {
	return newSpinner(p.stderr, p.color, p.level, msg)
}

func (p *printer) Spinnerf(format string, args ...any) Spinner {
	return newSpinner(p.stderr, p.color, p.level, p.format(format, args...))
}

func (p *printer) StdOut() io.Writer {
	return p.stdout
}

func (p *printer) StdErr() io.Writer {
	return p.stderr
}

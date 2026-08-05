package termio

import (
	"bufio"
	"fmt"
	"io"
	"time"
)

const spinnerInterval = 100 * time.Millisecond

type printer struct {
	stdin       io.Reader
	stdinReader *bufio.Reader
	stdout      io.Writer
	stderr      io.Writer
	color       bool
	level       Level
	assumeYes   bool
	isTerminal  func(int) bool
}

func (p *printer) write(w io.Writer, color, msg string) {
	if p.color {
		fmt.Fprintf(w, "%s%s%s\n", color, msg, ansiReset)
		return
	}
	fmt.Fprintln(w, msg)
}

func (p *printer) format(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func (p *printer) Info(msg string) {
	if p.level == LevelQuiet {
		return
	}
	p.write(p.stderr, "", msg)
}

func (p *printer) Infof(format string, args ...any) {
	if p.level == LevelQuiet {
		return
	}
	p.write(p.stderr, "", p.format(format, args...))
}

func (p *printer) Warn(msg string) {
	p.write(p.stderr, ansiYellow, msg)
}

func (p *printer) Warnf(format string, args ...any) {
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
	p.write(p.stderr, "", msg)
}

func (p *printer) Verbosef(format string, args ...any) {
	if p.level < LevelVerbose {
		return
	}
	p.write(p.stderr, "", p.format(format, args...))
}

func (p *printer) Out(msg string) {
	if p.level == LevelQuiet {
		return
	}
	p.write(p.stdout, "", msg)
}

func (p *printer) Outf(format string, args ...any) {
	if p.level == LevelQuiet {
		return
	}
	p.write(p.stdout, "", p.format(format, args...))
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

package output

import (
	"fmt"
	"io"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
)

type Level int

const (
	LevelNormal Level = iota
	LevelQuiet
	LevelVerbose
)

type Printer struct {
	w     io.Writer
	color bool
	level Level
}

func NewPrinter(w io.Writer, color bool) *Printer {
	return &Printer{w: w, color: color, level: LevelNormal}
}

func NewPrinterWithLevel(w io.Writer, color bool, level Level) *Printer {
	return &Printer{w: w, color: color, level: level}
}

func (l *Printer) Level() Level { return l.level }

func (l *Printer) write(color, msg string) {
	if l.color {
		fmt.Fprintf(l.w, "%s%s%s\n", color, msg, ansiReset)
	} else {
		fmt.Fprintln(l.w, msg)
	}
}

func (l *Printer) Infof(format string, args ...any) {
	if l.level == LevelQuiet {
		return
	}
	l.write("", l.format(format, args...))
}

func (l *Printer) Warnf(format string, args ...any) {
	l.write(ansiYellow, l.format(format, args...))
}

func (l *Printer) Errorf(format string, args ...any) { l.write(ansiRed, l.format(format, args...)) }

func (l *Printer) Debugf(format string, args ...any) {
	if l.level < LevelVerbose {
		return
	}
	l.write("", l.format(format, args...))
}

func (l *Printer) format(msg string, args ...any) string {
	return fmt.Sprintf(msg, args...)
}

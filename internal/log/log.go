package log

import (
	"fmt"
	"io"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
)

type Logger struct {
	w     io.Writer
	color bool
}

func New(w io.Writer, color bool) *Logger {
	return &Logger{w: w, color: color}
}

func (l *Logger) write(color, msg string) {
	if l.color {
		fmt.Fprintf(l.w, "%s%s%s\n", color, msg, ansiReset)
	} else {
		fmt.Fprintln(l.w, msg)
	}
}

func (l *Logger) Info(msg string)  { l.write("", msg) }
func (l *Logger) Warn(msg string)  { l.write(ansiYellow, msg) }
func (l *Logger) Error(msg string) { l.write(ansiRed, msg) }

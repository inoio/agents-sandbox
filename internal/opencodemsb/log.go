package opencodemsb

import (
	"fmt"
	"io"
	"time"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
)

type logger struct {
	w     io.Writer
	color bool
}

func newLogger(w io.Writer, color bool) *logger {
	return &logger{w: w, color: color}
}

func (l *logger) write(color, msg string) {
	if l.color {
		fmt.Fprintf(l.w, "%s%s%s\n", color, msg, ansiReset)
	} else {
		fmt.Fprintln(l.w, msg)
	}
}

func (l *logger) Info(msg string)  { l.write("", msg) }
func (l *logger) Warn(msg string)  { l.write(ansiYellow, msg) }
func (l *logger) Error(msg string) { l.write(ansiRed, msg) }

func (l *logger) Timing(label string, elapsed time.Duration) {
	fmt.Fprintf(l.w, "[timing] %s: %.3fs\n", label, elapsed.Seconds())
}

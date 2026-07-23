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

type Level int

const (
	LevelNormal Level = iota
	LevelQuiet
	LevelVerbose
)

type Logger struct {
	w     io.Writer
	color bool
	level Level
}

func New(w io.Writer, color bool) *Logger {
	return &Logger{w: w, color: color, level: LevelNormal}
}

func NewWithLevel(w io.Writer, color bool, level Level) *Logger {
	return &Logger{w: w, color: color, level: level}
}

func (l *Logger) Level() Level { return l.level }

func (l *Logger) write(color, msg string) {
	if l.color {
		fmt.Fprintf(l.w, "%s%s%s\n", color, msg, ansiReset)
	} else {
		fmt.Fprintln(l.w, msg)
	}
}

func (l *Logger) Info(msg string) {
	if l.level == LevelQuiet {
		return
	}
	l.write("", msg)
}

func (l *Logger) Warn(msg string) {
	l.write(ansiYellow, msg)
}

func (l *Logger) Error(msg string) { l.write(ansiRed, msg) }

func (l *Logger) Debug(msg string) {
	if l.level < LevelVerbose {
		return
	}
	l.write("", msg)
}

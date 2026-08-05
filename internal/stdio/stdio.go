package stdio

import (
	"io"

	"golang.org/x/term"
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

type Choice struct {
	Label       string
	Key         string
	Description string
}

type Spinner interface {
	Stop()
	StopError(err error)
}

type UI interface {
	Info(msg string)
	Infof(format string, args ...any)
	Warn(msg string)
	Warnf(format string, args ...any)
	Error(msg string, err error)
	Errorf(format string, args ...any)
	Verbose(msg string)
	Verbosef(format string, args ...any)
	Out(msg string)
	Outf(format string, args ...any)
	Spinner(msg string) Spinner
	Spinnerf(format string, args ...any) Spinner

	StdOut() io.Writer
	StdErr() io.Writer

	IsInteractive() bool
	Select(prompt string, choices []Choice, defaultKey string) (string, error)
	ConfirmDefault(prompt string, defaultYes bool) (bool, error)
	Input(prompt, defaultValue string) (string, error)
}

// New creates a production ui backed by the given streams.
func New(stdin io.Reader, stdout, stderr io.Writer, color bool, level Level, assumeYes bool) UI {
	//nolint:exhaustruct // stdinReader not needed in production
	return &printer{
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		color:      color,
		level:      level,
		assumeYes:  assumeYes,
		isTerminal: term.IsTerminal,
	}
}

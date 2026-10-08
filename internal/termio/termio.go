package termio

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/term"
)

// Level selects the minimum severity shown on the console. Levels are
// monotonic: showing a level also shows every more severe level, so a higher
// level is never hidden while a lower one is shown.
type Level int

const (
	LevelError Level = iota
	LevelWarning
	LevelInfo
	LevelVerbose
)

const (
	verboseString = "verbose"
	infoString    = "info"
	warningString = "warning"
	errorString   = "error"
)

// String returns the canonical lower-case name of the level.
func (l Level) String() string {
	switch l {
	case LevelError:
		return errorString
	case LevelWarning:
		return warningString
	case LevelVerbose:
		return verboseString
	case LevelInfo:
		return infoString
	default:
		return infoString
	}
}

// ParseLevel maps a case-insensitive level name to a Level.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(s) {
	case errorString:
		return LevelError, nil
	case warningString:
		return LevelWarning, nil
	case infoString:
		return LevelInfo, nil
	case verboseString:
		return LevelVerbose, nil
	default:
		return LevelInfo, fmt.Errorf("invalid log level %q (want error, warning, info or verbose)", s)
	}
}

type Choice struct {
	Label       string
	Key         string
	Description string
}

// PromptBackend selects how interactive Select/Input prompts are rendered.
type PromptBackend int

const (
	// PromptLine is the original line-based backend: prompts are printed as
	// lines and the user types a choice key. This is the default.
	PromptLine PromptBackend = iota
	// PromptHuh renders an arrow-key menu using charmbracelet/huh. It needs a
	// real terminal.
	PromptHuh
	// PromptHuhAccessible uses huh's line-based, screen-reader-friendly mode
	// (numbered options). It works with a plain reader.
	PromptHuhAccessible
)

// ParsePromptBackend maps a backend name to a PromptBackend. An empty name
// selects PromptLine.
func ParsePromptBackend(name string) (PromptBackend, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "line":
		return PromptLine, nil
	case "huh":
		return PromptHuh, nil
	case "huh-accessible":
		return PromptHuhAccessible, nil
	default:
		return PromptLine, fmt.Errorf("invalid prompt backend %q (want line, huh or huh-accessible)", name)
	}
}

// Option configures the production printer created by New.
type Option func(*printer)

// WithPromptBackend selects the interactive prompt backend.
func WithPromptBackend(backend PromptBackend) Option {
	return func(p *printer) {
		p.promptBackend = backend
	}
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
	Header(msg string)
	NewTable(headers ...string) *Table
	Spinner(msg string) Spinner
	Spinnerf(format string, args ...any) Spinner

	SetLevel(level Level)
	SetAssumeYes(assumeYes bool)
	SetQuiet(quiet bool)

	StdOut() io.Writer
	StdErr() io.Writer

	IsInteractive() bool
	Select(prompt string, choices []Choice, defaultKey string) (string, error)
	Input(prompt, defaultValue string) (string, error)
}

// New creates a production ui backed by the given streams.
func New(
	stdin io.Reader,
	stdout, stderr io.Writer,
	color bool,
	level Level,
	quiet bool,
	assumeYes bool,
	opts ...Option,
) UI {
	//nolint:exhaustruct_v5 // stdinReader not needed in production
	p := &printer{
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		color:      color,
		level:      level,
		quiet:      quiet,
		assumeYes:  assumeYes,
		isTerminal: term.IsTerminal,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

type OutToVerboseRedirect struct {
	UI
}

func (v *OutToVerboseRedirect) Out(msg string) { v.Verbose(msg) }
func (v *OutToVerboseRedirect) Outf(format string, args ...any) {
	v.Verbosef(format, args...)
}

package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"

	"golang.org/x/term"
)

const maxRetries = 5

var (
	AssumeYes bool //nolint:gochecknoglobals // CLI flag override, set once at startup

	// stdin and isTerminalFunc are overridable for testing.
	stdin          io.Reader = os.Stdin        //nolint:gochecknoglobals // test seam, swapped via SetStdinForTesting
	isTerminalFunc           = term.IsTerminal //nolint:gochecknoglobals // test seam, swapped via SetStdinForTesting

	// stdinReader is used by tests that drive multiple prompts in sequence;
	// a single buffered reader prevents input from being lost between prompts.
	stdinReader *bufio.Reader //nolint:gochecknoglobals // test seam, swapped via SetStdinForTesting
)

func IsInteractive() bool {
	return isTerminalFunc(int(os.Stdin.Fd())) && !AssumeYes
}

// SetStdinForTesting replaces the prompt input source and forces interactive
// mode for the duration of a test. The returned function restores the previous
// state and should be deferred by the caller.
func SetStdinForTesting(r io.Reader) func() {
	oldStdin := stdin
	oldStdinReader := stdinReader
	oldIsTerminal := isTerminalFunc
	stdin = r
	stdinReader = bufio.NewReader(r)
	isTerminalFunc = func(int) bool { return true }
	return func() {
		stdin = oldStdin
		stdinReader = oldStdinReader
		isTerminalFunc = oldIsTerminal
	}
}

func getStdinReader() *bufio.Reader {
	if stdinReader != nil {
		return stdinReader
	}
	return bufio.NewReader(stdin)
}

type Choice struct {
	Label       string
	Key         string
	Description string
}

func Select(prompt string, choices []Choice, defaultKey string, logger *log.Logger) (string, error) {
	if !IsInteractive() {
		logger.Info(fmt.Sprintf("%s: using default '%s'", prompt, defaultKey))
		return defaultKey, nil
	}

	fmt.Fprintf(os.Stderr, "%s\n", prompt)
	for _, c := range choices {
		fmt.Fprintf(os.Stderr, "  %s) %s - %s\n", c.Key, c.Label, c.Description)
	}
	fmt.Fprintf(os.Stderr, "default [%s]: ", defaultKey)

	keys := make(map[string]string, len(choices))
	for _, c := range choices {
		keys[strings.ToLower(c.Key)] = c.Key
	}

	reader := getStdinReader()
	for range maxRetries {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read selection: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultKey, nil
		}
		if key, ok := keys[strings.ToLower(line)]; ok {
			return key, nil
		}
		fmt.Fprintf(os.Stderr, "invalid input '%s', please try again: ", line)
	}

	return "", errors.New("too many invalid selections")
}

func ConfirmDefault(prompt string, defaultYes bool, logger *log.Logger) (bool, error) {
	if !IsInteractive() {
		defaultValue := "n"
		if defaultYes {
			defaultValue = "y"
		}
		logger.Info(fmt.Sprintf("%s: using default '%s'", prompt, defaultValue))
		return defaultYes, nil
	}

	defaultHint := "y/N"
	if defaultYes {
		defaultHint = "Y/n"
	}
	fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, defaultHint)

	reader := getStdinReader()
	for range maxRetries {
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, fmt.Errorf("read confirmation: %w", err)
		}
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			return defaultYes, nil
		}
		if line == "y" || line == "yes" {
			return true, nil
		}
		if line == "n" || line == "no" {
			return false, nil
		}
		fmt.Fprintf(os.Stderr, "please answer y or n: ")
	}

	return false, errors.New("too many invalid confirmations")
}

func Input(prompt, defaultValue string, logger *log.Logger) (string, error) {
	if !IsInteractive() {
		logger.Info(fmt.Sprintf("%s: using default '%s'", prompt, defaultValue))
		return defaultValue, nil
	}

	fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, defaultValue)

	reader := getStdinReader()
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

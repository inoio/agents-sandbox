package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
	"golang.org/x/term"
)

const maxRetries = 5

var (
	AssumeYes bool

	// stdin and isTerminalFunc are overridable for testing.
	stdin          io.Reader = os.Stdin
	isTerminalFunc           = term.IsTerminal
)

func IsInteractive() bool {
	return isTerminalFunc(int(os.Stdin.Fd())) && !AssumeYes
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

	reader := bufio.NewReader(stdin)
	for i := 0; i < maxRetries; i++ {
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

	return "", fmt.Errorf("too many invalid selections")
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

	reader := bufio.NewReader(stdin)
	for i := 0; i < maxRetries; i++ {
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

	return false, fmt.Errorf("too many invalid confirmations")
}

func Input(prompt, defaultValue string, logger *log.Logger) (string, error) {
	if !IsInteractive() {
		logger.Info(fmt.Sprintf("%s: using default '%s'", prompt, defaultValue))
		return defaultValue, nil
	}

	fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, defaultValue)

	reader := bufio.NewReader(stdin)
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

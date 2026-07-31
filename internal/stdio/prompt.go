package stdio

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

func (p *printer) getStdinReader() *bufio.Reader {
	if p.stdinReader == nil {
		p.stdinReader = bufio.NewReader(p.stdin)
	}
	return p.stdinReader
}

func (p *printer) IsInteractive() bool {
	if p.stdin == nil {
		return p.isTerminal(int(os.Stdin.Fd())) && !p.assumeYes
	}
	return p.isTerminal(0) && !p.assumeYes
}

const maxRetries = 5

var errTooManyInvalidInputs = errors.New("too many invalid inputs")

func (p *printer) Select(prompt string, choices []Choice, defaultKey string) (string, error) {
	if !p.IsInteractive() {
		p.Successf("%s: using default '%s'", prompt, defaultKey)
		return defaultKey, nil
	}

	fmt.Fprintf(p.stderr, "%s\n", prompt)
	for _, c := range choices {
		desc := c.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(p.stderr, "  %s) %s - %s\n", c.Key, c.Label, desc)
	}
	p.Successf("  default [%s]", defaultKey)

	keys := make(map[string]string, len(choices))
	for _, c := range choices {
		keys[strings.ToLower(c.Key)] = c.Key
	}

	reader := p.getStdinReader()
	for range maxRetries {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultKey, nil
		}
		if key, ok := keys[strings.ToLower(line)]; ok {
			return key, nil
		}
		fmt.Fprintf(p.stderr, "invalid input '%s', please try again: ", line)
	}

	return "", errTooManyInvalidInputs
}

func (p *printer) ConfirmDefault(prompt string, defaultYes bool) (bool, error) {
	if !p.IsInteractive() {
		p.Successf("%s: using default '%t'", prompt, defaultYes)
		return defaultYes, nil
	}

	defaultHint := "y/N"
	if defaultYes {
		defaultHint = "Y/n"
	}
	p.Successf("%s [%s]", prompt, defaultHint)

	reader := p.getStdinReader()
	for range maxRetries {
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, err
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
		fmt.Fprintf(p.stderr, "please answer y or n: ")
	}

	return false, errTooManyInvalidInputs
}

func (p *printer) Input(prompt, defaultValue string) (string, error) {
	if !p.IsInteractive() {
		p.Successf("%s: using default '%s'", prompt, defaultValue)
		return defaultValue, nil
	}

	fmt.Fprintf(p.stderr, "%s [%s]: ", prompt, defaultValue)

	reader := p.getStdinReader()
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

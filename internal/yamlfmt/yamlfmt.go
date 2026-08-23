// Package yamlfmt formats YAML parse errors for user-facing output.
package yamlfmt

import (
	"fmt"
	"regexp"
)

// lineRe matches the line/column detail embedded in a yaml parse error message.
var lineRe = regexp.MustCompile(`line \d+(, column \d+)?`)

// WrapErr returns a user-friendly error for a YAML parse failure in filename,
// retaining the technical detail (which line/column produced the error) via %w.
func WrapErr(filename string, err error) error {
	if line := lineRe.FindString(err.Error()); line != "" {
		return fmt.Errorf("%s contains invalid YAML (%s): %w", filename, line, err)
	}
	return fmt.Errorf("%s contains invalid YAML: %w", filename, err)
}

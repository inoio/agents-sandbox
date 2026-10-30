package cliout

import (
	"encoding/json"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// FormatJSON is the only supported value of the --format flag.
const FormatJSON = "json"

// Mode selects how a command renders its result.
type Mode int

const (
	ModeTable Mode = iota
	ModeIdentifiers
	ModeJSON
)

// ResolveMode selects a rendering mode from the output format flag and the
// global quiet flag. Table is the default; quiet renders identifiers only; an
// explicit format selects structured output. Quiet and an explicit format are
// mutually exclusive.
func ResolveMode(format string, quiet bool) (Mode, error) {
	switch {
	case format == "" && !quiet:
		return ModeTable, nil
	case format == "":
		return ModeIdentifiers, nil
	case format != FormatJSON:
		return 0, fmt.Errorf("unsupported format %q: only %q is supported", format, FormatJSON)
	case quiet:
		return 0, fmt.Errorf("--format %s and --quiet are mutually exclusive", FormatJSON)
	default:
		return ModeJSON, nil
	}
}

// WriteJSON writes value as indented JSON on stdout. It is the shared
// structured-output path for list, inspect, and storage renderers.
func WriteJSON(ui termio.UI, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	ui.Out(string(data))
	return nil
}

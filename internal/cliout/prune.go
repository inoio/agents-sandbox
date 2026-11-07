package cliout

import (
	"encoding/json"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// RenderPruneReport writes the JSON value under --format json, and calls the
// human renderer for the table and identifiers-only modes. Quiet normally
// suppresses stdout, but a targeted purge's human renderer names its plan
// through the quiet-immune channel, so it must still run under ModeIdentifiers;
// the group prunes' renderers write through the quiet-gated methods and so stay
// silent. The values passed by callers are concrete report DTOs, so the JSON
// marshal cannot fail.
func RenderPruneReport(ui termio.Output, mode Mode, human func(), jsonValue any) {
	if mode == ModeJSON {
		data, _ := json.MarshalIndent(jsonValue, "", "  ")
		ui.Out(string(data))
		return
	}
	human()
}

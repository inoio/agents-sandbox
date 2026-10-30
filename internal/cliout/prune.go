package cliout

import (
	"encoding/json"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// RenderPruneReport writes the JSON value under --format json, or calls the
// human renderer by default. Quiet suppresses stdout entirely (prune has no
// identifiers-only mode). The values passed by callers are concrete report
// DTOs, so the JSON marshal cannot fail.
func RenderPruneReport(ui termio.UI, mode Mode, human func(), jsonValue any) {
	if mode == ModeJSON {
		data, _ := json.MarshalIndent(jsonValue, "", "  ")
		ui.Out(string(data))
		return
	}
	if mode == ModeTable {
		human()
	}
}

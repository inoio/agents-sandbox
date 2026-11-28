package journeys

import (
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// catalogueFileName is the generated scenario catalogue beside the suite.
const catalogueFileName = "JOURNEYS.md"

// renderCatalogue renders the journey table as Markdown: per journey, the
// harnesses it drives, the ones still exempted (with the reason), and its
// steps as a Gherkin scenario. It is generated, so it cannot drift from the
// table.
func renderCatalogue(journeys []Journey, exemptions map[string]map[string]string) string {
	var text strings.Builder
	text.WriteString("# msb journeys\n\n")
	text.WriteString("Generated from the journey table (`Journeys`, `Exemptions`) by `TestJourneyCatalogueIsCurrent`;")
	text.WriteString(" do not edit. Steps marked `# check` are recorded on failure and the journey goes on.\n")
	text.WriteString("Run `go test -tags microsandbox_ffi_path ./test/msb/journeys/ -run TestJourneyCatalogueIsCurrent")
	text.WriteString(" -args -update-catalogue`")
	text.WriteString(" to regenerate it. See `.operator-shared/specs/testing/msb-journeys.md`.\n")
	for _, journey := range journeys {
		text.WriteString("\n## " + journey.Name + "\n\n")
		text.WriteString("Runs on: " + strings.Join(journey.Harnesses, ", ") + "\n")
		if journey.Network != "" {
			text.WriteString("\nNetwork profile: `" + journey.Network + "`\n")
		}
		if journey.SerialHarnesses {
			text.WriteString("\nHarnesses run serially (shared host callback ports).\n")
		}
		writeExemptions(&text, exemptions[journey.Name])
		text.WriteString("\n```gherkin\nScenario: " + journey.Name + "\n")
		for _, step := range journey.Steps {
			if step.Kind == Check {
				text.WriteString("  # check\n")
			}
			text.WriteString("  " + step.Text() + "\n")
		}
		text.WriteString("```\n")
	}
	return text.String()
}

// writeExemptions lists a journey's exempted harnesses grouped by reason, in
// registry order.
func writeExemptions(text *strings.Builder, reasons map[string]string) {
	if len(reasons) == 0 {
		return
	}
	byReason := map[string][]string{}
	var order []string
	for _, name := range agent.Names() {
		reason, ok := reasons[name]
		if !ok {
			continue
		}
		if !slices.Contains(order, reason) {
			order = append(order, reason)
		}
		byReason[reason] = append(byReason[reason], name)
	}
	text.WriteString("\nNot yet:\n\n")
	for _, reason := range order {
		text.WriteString("- " + strings.Join(byReason[reason], ", ") + ": " + reason + "\n")
	}
}

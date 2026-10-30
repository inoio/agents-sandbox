package cliout

import "github.com/inoio/agents-sandbox/internal/termio"

// Printer shapes one item type for list output. The first cell is the item's
// identifier, printed alone in identifiers mode.
type Printer[T any] interface {
	Headers() []string
	Cells(item T) []string
	JSON(items []T) any
}

// PrintList renders items in the selected mode: identifiers, JSON, or the
// default aligned table. An empty table prints emptyMessage instead.
func PrintList[T any](ui termio.UI, mode Mode, items []T, emptyMessage string, printer Printer[T]) error {
	switch mode {
	case ModeIdentifiers:
		for _, item := range items {
			ui.Print(printer.Cells(item)[0])
		}
	case ModeJSON:
		return WriteJSON(ui, printer.JSON(items))
	case ModeTable:
		if len(items) == 0 {
			ui.Info(emptyMessage)
			return nil
		}
		table := ui.NewTable(printer.Headers()...)
		for _, item := range items {
			table.AddRow(printer.Cells(item)...)
		}
		table.Print()
	}
	return nil
}

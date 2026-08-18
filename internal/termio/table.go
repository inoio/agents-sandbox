package termio

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// tableGap is the number of spaces placed between columns.
const tableGap = 4

// Table renders column-aligned rows matching the microsandbox (`msb`) CLI
// output style: each column is sized to the widest cell and columns are
// separated by a four-space gap. The header row is rendered bold and cyan when
// color is enabled. Alignment is computed on plain text before styling, so
// ANSI codes never shift columns. Build a Table with ui.NewTable, add rows with
// AddRow, then print with Print.
type Table struct {
	ui      UI
	headers []string
	rows    [][]string
}

// AddRow appends a data row. Rows longer than the header are truncated and
// shorter rows are padded with empty cells.
func (t *Table) AddRow(row ...string) {
	t.rows = append(t.rows, row)
}

// Print renders the table and writes it through the UI: the header line is
// styled via UI.Header and each data line via UI.Out. It is a no-op when there
// are no rows.
func (t *Table) Print() {
	if t.ui == nil {
		return
	}
	lines := strings.Split(t.render(), "\n")
	if len(lines) < 2 {
		return
	}
	t.ui.Header(lines[0])
	for _, line := range lines[1:] {
		t.ui.Out(line)
	}
}

// render returns the aligned table without trailing newlines, or an empty
// string when there are no rows.
func (t *Table) render() string {
	if len(t.rows) == 0 {
		return ""
	}
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = displayWidth(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], displayWidth(cell))
			}
		}
	}

	lines := []string{renderRow(t.headers, widths)}
	for _, row := range t.rows {
		lines = append(lines, renderRow(paddedRow(row, len(t.headers)), widths))
	}
	return strings.Join(lines, "\n")
}

// paddedRow pads a row to the header length with empty cells.
func paddedRow(row []string, n int) []string {
	out := make([]string, n)
	copy(out, row)
	return out
}

// renderRow left-pads every cell except the last to its column width, adding
// the inter-column gap after each non-last cell.
func renderRow(row []string, widths []int) string {
	var b strings.Builder
	for i, cell := range row {
		if i == len(widths)-1 {
			b.WriteString(cell)
			continue
		}
		padding := max(widths[i]-displayWidth(cell)+tableGap, 0)
		fmt.Fprintf(&b, "%s%s", cell, strings.Repeat(" ", padding))
	}
	return b.String()
}

// displayWidth returns the visible width of s for alignment purposes. ANSI
// codes are stripped first so styled cells (e.g. colored statuses) align with
// plain text, matching msb's console::measure_text_width.
func displayWidth(s string) int {
	return utf8.RuneCountInString(stripANSICodes(s))
}

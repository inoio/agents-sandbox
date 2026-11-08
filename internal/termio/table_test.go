package termio

import (
	"strings"
	"testing"
)

func TestRenderAlignsColumns(t *testing.T) {
	tbl := NewTableFor(&minimalTableWriter{}, []string{"NAME", "STATUS", "CREATED"})
	tbl.AddRow("alpha", "running", "2026-08-17 09:00:00")
	tbl.AddRow("agents-sandbox-vm-beta", "stopped", "2026-08-16 12:00:00")

	rendered := tbl.render()
	if rendered == "" {
		t.Fatal("render() returned empty")
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("render() = %d lines, want 3: %q", len(lines), rendered)
	}
	// Column width is set by the widest cell (the long name), so the shorter
	// NAME cell is padded to align STATUS across rows.
	if !strings.HasPrefix(lines[1], "alpha") {
		t.Errorf("row 1 should start with alpha: %q", lines[1])
	}
	if !strings.Contains(lines[1], "running") {
		t.Errorf("row 1 should contain status: %q", lines[1])
	}
	if !strings.HasSuffix(lines[2], "2026-08-16 12:00:00") {
		t.Errorf("last cell should be unpadded and last: %q", lines[2])
	}
}

func TestRenderUsesWidestCellPerColumn(t *testing.T) {
	tbl := NewTableFor(&minimalTableWriter{}, []string{"A", "B"})
	tbl.AddRow("short", "1")
	tbl.AddRow("a-very-long-name", "2")

	rendered := tbl.render()
	lines := strings.Split(rendered, "\n")
	// Column A is wide enough for the longest value, so column B lines up.
	idx1 := strings.Index(lines[1], "1")
	idx2 := strings.Index(lines[2], "2")
	if idx1 != idx2 {
		t.Errorf("column B not aligned: row1=%d row2=%d", idx1, idx2)
	}
}

func TestRenderEmptyWhenNoRows(t *testing.T) {
	tbl := NewTableFor(&minimalTableWriter{}, []string{"NAME"})
	if got := tbl.render(); got != "" {
		t.Errorf("render() with no rows = %q, want empty", got)
	}
}

func TestRenderLastColumnUnpadded(t *testing.T) {
	tbl := NewTableFor(&minimalTableWriter{}, []string{"A", "B"})
	tbl.AddRow("x", "y")
	if got := tbl.render(); !strings.HasSuffix(got, "y") {
		t.Errorf("last column should be unpadded, got %q", got)
	}
}

func TestRenderTruncatesWideRowsAndPadsShortRows(t *testing.T) {
	tbl := NewTableFor(&minimalTableWriter{}, []string{"A", "B"})
	tbl.AddRow("one", "two", "three") // extra column ignored
	tbl.AddRow("only-a")              // missing column padded

	rendered := tbl.render()
	lines := strings.Split(rendered, "\n")
	if !strings.Contains(lines[1], "two") {
		t.Errorf("extra column content should be truncated: %q", lines[1])
	}
	// Missing second column is padded as empty, so the row ends at column A.
	if !strings.HasPrefix(lines[2], "only-a") {
		t.Errorf("short row should keep only-a in column A: %q", lines[2])
	}
}

func TestPrintWritesHeaderAndRows(t *testing.T) {
	w := &minimalTableWriter{}
	tbl := NewTableFor(w, []string{"NAME", "STATUS"})
	tbl.AddRow("alpha", "running")
	tbl.AddRow("beta", "stopped")

	tbl.Print()

	// The header goes through Header; each row goes through Out.
	if len(w.headers) != 1 {
		t.Fatalf("Print() recorded %d header calls, want 1: %v", len(w.headers), w.headers)
	}
	if len(w.rows) != 2 {
		t.Fatalf("Print() recorded %d row calls, want 2: %v", len(w.rows), w.rows)
	}
	if !strings.Contains(w.headers[0], "NAME") || !strings.Contains(w.headers[0], "STATUS") {
		t.Errorf("header call should contain the headers: %q", w.headers[0])
	}
	if !strings.Contains(w.rows[0], "alpha") {
		t.Errorf("first row call should be the first row: %q", w.rows[0])
	}
}

func TestPrintNoopWithoutUI(t *testing.T) {
	// A Table constructed without a UI must not panic when printed; the guard
	// keeps callers that build a table before attaching output safe.
	tbl := &Table{}
	tbl.AddRow("alpha")
	tbl.Print()
}

func TestPrintNoopWhenNoRows(t *testing.T) {
	w := &minimalTableWriter{}
	tbl := NewTableFor(w, []string{"NAME"})
	tbl.Print()
	if len(w.headers) != 0 || len(w.rows) != 0 {
		t.Errorf("Print() with no rows should write nothing, got headers %v rows %v", w.headers, w.rows)
	}
}

func TestRenderAlignsStyledCellsWithPlainCells(t *testing.T) {
	tbl := NewTableFor(&minimalTableWriter{}, []string{"NAME", "STATUS", "CREATED"})
	tbl.AddRow("alpha", StyleStatus("running"), "2026-08-17 09:00:00")
	tbl.AddRow("beta", "unknown", "2026-08-17 10:00:00")

	rendered := tbl.render()
	lines := strings.Split(rendered, "\n")
	// Compare visible columns, so ANSI bytes in the styled cell don't skew the
	// byte offset of the CREATED column.
	idx1 := strings.Index(StripANSICodes(lines[1]), "09:00:00")
	idx2 := strings.Index(StripANSICodes(lines[2]), "10:00:00")
	if idx1 != idx2 {
		t.Errorf("CREATED column not aligned with styled status: row1=%d row2=%d", idx1, idx2)
	}
}

// minimalTableWriter implements only the two methods a Table prints through,
// proving a Table no longer depends on the whole UI.
type minimalTableWriter struct {
	headers []string
	rows    []string
}

func (w *minimalTableWriter) Header(msg string) { w.headers = append(w.headers, msg) }
func (w *minimalTableWriter) Out(msg string)    { w.rows = append(w.rows, msg) }

func TestTablePrintsThroughMinimalWriter(t *testing.T) {
	w := &minimalTableWriter{}
	table := &Table{ui: w, headers: []string{"NAME"}}
	table.AddRow("alpha")
	table.Print()

	if len(w.headers) != 1 || w.headers[0] != "NAME" {
		t.Fatalf("header calls = %q, want [NAME]", w.headers)
	}
	if len(w.rows) != 1 || w.rows[0] != "alpha" {
		t.Fatalf("row calls = %q, want [alpha]", w.rows)
	}
}

// TestNewTableForRecordsThroughUI pins the exported constructor a test double
// (or any writer implementing TableWriter) uses to build a Table that renders
// through it, without reaching into the Table's unexported fields.
func TestNewTableForRecordsThroughUI(t *testing.T) {
	w := &minimalTableWriter{}
	table := NewTableFor(w, []string{"NAME"})
	if table == nil {
		t.Fatal("nil table")
	}
	table.AddRow("alpha")
	table.Print()

	if len(w.headers) != 1 || w.headers[0] != "NAME" {
		t.Fatalf("header calls = %q, want [NAME]", w.headers)
	}
	if len(w.rows) != 1 || w.rows[0] != "alpha" {
		t.Fatalf("row calls = %q, want [alpha]", w.rows)
	}
}

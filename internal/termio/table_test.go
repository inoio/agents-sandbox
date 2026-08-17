package termio

import (
	"strings"
	"testing"
)

func TestRenderAlignsColumns(t *testing.T) {
	tbl := (&Mock{}).NewTable("NAME", "STATUS", "CREATED")
	tbl.AddRow("alpha", "running", "2026-08-17 09:00:00")
	tbl.AddRow("opencode-sandbox-vm-beta", "stopped", "2026-08-16 12:00:00")

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
	tbl := (&Mock{}).NewTable("A", "B")
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
	tbl := (&Mock{}).NewTable("NAME")
	if got := tbl.render(); got != "" {
		t.Errorf("render() with no rows = %q, want empty", got)
	}
}

func TestRenderLastColumnUnpadded(t *testing.T) {
	tbl := (&Mock{}).NewTable("A", "B")
	tbl.AddRow("x", "y")
	if got := tbl.render(); !strings.HasSuffix(got, "y") {
		t.Errorf("last column should be unpadded, got %q", got)
	}
}

func TestRenderTruncatesWideRowsAndPadsShortRows(t *testing.T) {
	tbl := (&Mock{}).NewTable("A", "B")
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
	m := &Mock{}
	tbl := m.NewTable("NAME", "STATUS")
	tbl.AddRow("alpha", "running")
	tbl.AddRow("beta", "stopped")

	tbl.Print()

	// Header is the first OutCall; rows follow.
	if len(m.OutCalls) != 3 {
		t.Fatalf("Print() recorded %d OutCalls, want 3: %v", len(m.OutCalls), m.OutCalls)
	}
	if !strings.Contains(m.OutCalls[0], "NAME") || !strings.Contains(m.OutCalls[0], "STATUS") {
		t.Errorf("first call should be header: %q", m.OutCalls[0])
	}
	if !strings.Contains(m.OutCalls[1], "alpha") {
		t.Errorf("second call should be first row: %q", m.OutCalls[1])
	}
}

func TestPrintNoopWhenNoRows(t *testing.T) {
	m := &Mock{}
	tbl := m.NewTable("NAME")
	tbl.Print()
	if len(m.OutCalls) != 0 {
		t.Errorf("Print() with no rows should write nothing, got %v", m.OutCalls)
	}
}

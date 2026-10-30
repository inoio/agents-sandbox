package cliout

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
)

type stringPrinter struct {
	jsonValue func([]string) any
}

func (stringPrinter) Headers() []string { return []string{"VALUE", "LENGTH"} }
func (stringPrinter) Cells(item string) []string {
	return []string{item, strings.Repeat("x", len(item))}
}
func (p stringPrinter) JSON(items []string) any { return p.jsonValue(items) }

func identityJSON(items []string) any { return append(make([]string, 0, len(items)), items...) }

func TestPrintListIdentifiersPrintsFirstCell(t *testing.T) {
	ui := &termio.Mock{}
	if err := PrintList(ui, ModeIdentifiers, []string{"a", "bb"}, "none", stringPrinter{identityJSON}); err != nil {
		t.Fatalf("PrintList: %v", err)
	}
	if strings.Join(ui.PrintCalls, ",") != "a,bb" {
		t.Errorf("identifiers = %v, want [a bb]", ui.PrintCalls)
	}
}

func TestPrintListTableEmptyShowsMessage(t *testing.T) {
	ui := &termio.Mock{}
	if err := PrintList(ui, ModeTable, []string{}, "none found", stringPrinter{identityJSON}); err != nil {
		t.Fatalf("PrintList: %v", err)
	}
	if len(ui.InfoCalls) != 1 || ui.InfoCalls[0] != "none found" {
		t.Errorf("InfoCalls = %v, want [none found]", ui.InfoCalls)
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("empty table should not write rows, got %v", ui.OutCalls)
	}
}

func TestPrintListTableRendersHeadersAndCells(t *testing.T) {
	ui := &termio.Mock{}
	if err := PrintList(ui, ModeTable, []string{"abc"}, "none", stringPrinter{identityJSON}); err != nil {
		t.Fatalf("PrintList: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	for _, want := range []string{"VALUE", "LENGTH", "abc", "xxx"} {
		if !strings.Contains(joined, want) {
			t.Errorf("table output missing %q: %q", want, joined)
		}
	}
}

func TestPrintListJSONMarshalError(t *testing.T) {
	ui := &termio.Mock{}
	printer := stringPrinter{func([]string) any { return func() {} }}
	if err := PrintList(ui, ModeJSON, []string{"x"}, "none", printer); err == nil {
		t.Fatal("expected a marshal error for a non-marshalable JSON value")
	}
}

func TestPrintListJSONEmpty(t *testing.T) {
	ui := &termio.Mock{}
	if err := PrintList(ui, ModeJSON, []string{}, "none", stringPrinter{identityJSON}); err != nil {
		t.Fatalf("PrintList: %v", err)
	}
	if strings.TrimSpace(strings.Join(ui.OutCalls, "\n")) != "[]" {
		t.Errorf("empty JSON list should render [], got %q", ui.OutCalls)
	}
}

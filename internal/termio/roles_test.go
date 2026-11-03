package termio

import "testing"

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

// prompterOnly implements Prompter alone: ConfirmAction must accept it.
type prompterOnly struct{ confirmed bool }

func (p *prompterOnly) IsInteractive() bool { return true }
func (p *prompterOnly) Select(string, []Choice, string) (string, error) {
	return "", nil
}
func (p *prompterOnly) Input(string, string) (string, error) { return "", nil }
func (p *prompterOnly) Confirm(string, bool) (bool, error) {
	p.confirmed = true
	return true, nil
}
func (p *prompterOnly) Acknowledge(string) error { return nil }

func TestConfirmActionAcceptsPrompterOnly(t *testing.T) {
	p := &prompterOnly{}
	if err := ConfirmAction(p, "remove?"); err != nil {
		t.Fatalf("ConfirmAction: %v", err)
	}
	if !p.confirmed {
		t.Fatal("Confirm was not called")
	}
}

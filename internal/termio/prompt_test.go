package termio

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/term"
)

func TestIsInteractive(t *testing.T) {
	t.Run("returns false when stdin is not a terminal", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return false }
		if p.IsInteractive() {
			t.Fatal("expected false when stdin is not a terminal")
		}
	})

	t.Run("returns false when yes flag is set", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, true)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }
		if p.IsInteractive() {
			t.Fatal("expected false when yes flag is set")
		}
	})

	t.Run("returns true when stdin is a terminal and yes flag is not set", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }
		if !p.IsInteractive() {
			t.Fatal("expected true when stdin is a terminal and yes flag is not set")
		}
	})

	t.Run("SetAssumeYes makes it non-interactive", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }
		if !p.IsInteractive() {
			t.Fatal("expected interactive before SetAssumeYes")
		}
		ui.SetAssumeYes(true)
		if p.IsInteractive() {
			t.Fatal("expected non-interactive after SetAssumeYes")
		}
	})
}

func TestSecretInputRequiresInteractiveTerminal(t *testing.T) {
	ui := New(strings.NewReader("secret\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
	p := ui.(*printer)
	p.isTerminal = func(int) bool { return false }
	if _, err := ui.SecretInput("secret"); err == nil || !strings.Contains(err.Error(), "interactive") {
		t.Fatalf("SecretInput error = %v", err)
	}
}

func TestSecretInputWithPipeBackedStdinRejectsBeforeEcho(t *testing.T) {
	ui := New(strings.NewReader("secret\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
	p := ui.(*printer)
	p.isTerminal = func(int) bool { return true }
	if _, err := ui.SecretInput("secret"); err == nil || !strings.Contains(err.Error(), "terminal-backed") {
		t.Fatalf("SecretInput error = %v", err)
	}
}

func TestReadMaskedSecretEchoesStarsAndHandlesBackspace(t *testing.T) {
	var output bytes.Buffer
	value, err := readMaskedSecret(strings.NewReader("ab\bcd\n"), &output)
	if err != nil || value != "acd" {
		t.Fatalf("masked secret = %q, %v", value, err)
	}
	if output.String() != "**\b \b**" {
		t.Fatalf("masked output = %q", output.String())
	}
}

func TestReadMaskedSecretIgnoresControlAndInterrupts(t *testing.T) {
	var output bytes.Buffer
	value, err := readMaskedSecret(strings.NewReader("\x01a\x03"), &output)
	if err == nil || !strings.Contains(err.Error(), "interrupted") || value != "" {
		t.Fatalf("interrupt result = %q, %v", value, err)
	}
	if output.String() != "*" {
		t.Fatalf("control-key output = %q", output.String())
	}
}

func TestSecretInputRestoresTerminalAfterReaderError(t *testing.T) {
	stdin, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	t.Cleanup(func() { _ = stdin.Close() })
	oldMakeRaw, oldRestore := makeRawTerminal, restoreTerminal
	t.Cleanup(func() {
		makeRawTerminal = oldMakeRaw
		restoreTerminal = oldRestore
	})
	makeRawTerminal = func(int) (*term.State, error) { return &term.State{}, nil }
	restored := false
	restoreTerminal = func(int, *term.State) error { restored = true; return nil }
	ui := New(stdin, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
	p := ui.(*printer)
	p.isTerminal = func(int) bool { return true }
	// The process stdin is not readable in this test; raw setup and restoration
	// are verified before the read fails.
	_, _ = ui.SecretInput("secret")
	if !restored {
		t.Fatal("terminal state was not restored")
	}
}

func TestSecretInputReturnsRawModeError(t *testing.T) {
	stdin, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	_ = stdin.Close()
	oldMakeRaw := makeRawTerminal
	t.Cleanup(func() { makeRawTerminal = oldMakeRaw })
	makeRawTerminal = func(int) (*term.State, error) { return nil, errors.New("raw mode failed") }
	ui := New(stdin, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
	p := ui.(*printer)
	p.isTerminal = func(int) bool { return true }
	if _, err := ui.SecretInput("secret"); err == nil || !strings.Contains(err.Error(), "raw mode failed") {
		t.Fatalf("raw mode error = %v", err)
	}
}

func TestSelect(t *testing.T) {
	choices := []Choice{
		{Label: "Keep", Key: "k", Description: "Keep the worktree"},
		{Label: "Remove", Key: "r", Description: "Remove the worktree"},
	}

	t.Run("prints default info in interactive mode", func(t *testing.T) {
		var stderr bytes.Buffer
		ui := New(strings.NewReader("k\n"), &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }
		if _, err := ui.Select("What to do?", choices, "k"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stderr.String(), "default [k]") {
			t.Fatalf("interactive default output = %q", stderr.String())
		}
	})

	t.Run("returns default in non-interactive mode", func(t *testing.T) {
		var stderr bytes.Buffer
		ui := New(strings.NewReader("r\n"), &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "k" {
			t.Fatalf("expected default key k, got %q", got)
		}
		if !strings.Contains(stderr.String(), "using default 'k'") {
			t.Errorf("expected default message on stderr, got %q", stderr.String())
		}
	})

	t.Run("returns matched key in interactive mode", func(t *testing.T) {
		ui := New(strings.NewReader("r\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r, got %q", got)
		}
	})

	t.Run("matches keys case-insensitively", func(t *testing.T) {
		ui := New(strings.NewReader("R\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r, got %q", got)
		}
	})

	t.Run("marks the default choice inline", func(t *testing.T) {
		var stderr bytes.Buffer
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(), "k) Keep (default) - Keep the worktree") {
			t.Errorf("expected default-marked listing, got %q", stderr.String())
		}
		if strings.Contains(stderr.String(), "r) Remove (default)") {
			t.Errorf("non-default choice should not be marked default: %q", stderr.String())
		}
	})

	t.Run("uses default when user presses enter", func(t *testing.T) {
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "k" {
			t.Fatalf("expected default key k, got %q", got)
		}
	})

	t.Run("retries on invalid input", func(t *testing.T) {
		ui := New(strings.NewReader("x\nfoo\nr\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r after retries, got %q", got)
		}
	})

	t.Run("returns error after too many retries", func(t *testing.T) {
		ui := New(
			strings.NewReader("x\nx\nx\nx\nx\nx\n"),
			&bytes.Buffer{},
			&bytes.Buffer{},
			false,
			LevelInfo,
			false,
			false,
		)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Select("What to do?", choices, "k")
		if err == nil {
			t.Fatal("expected error after max retries")
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		ui := New(&failingReader{}, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Select("What to do?", choices, "k")
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})
}

func TestInput(t *testing.T) {
	t.Run("returns default in non-interactive mode", func(t *testing.T) {
		ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := ui.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Fatalf("expected default main, got %q", got)
		}
	})

	t.Run("returns user input", func(t *testing.T) {
		ui := New(strings.NewReader("feature\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "feature" {
			t.Fatalf("expected feature, got %q", got)
		}
	})

	t.Run("returns default on empty input", func(t *testing.T) {
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := ui.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Fatalf("expected default main, got %q", got)
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		ui := New(&failingReader{}, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false)
		p := ui.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := ui.Input("Branch name:", "main")
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})
}

type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

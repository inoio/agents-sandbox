package stdio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestIsInteractive(t *testing.T) {
	t.Run("returns false when stdin is not a terminal", func(t *testing.T) {
		io := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return false }
		if p.IsInteractive() {
			t.Fatal("expected false when stdin is not a terminal")
		}
	})

	t.Run("returns false when yes flag is set", func(t *testing.T) {
		io := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, true)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }
		if p.IsInteractive() {
			t.Fatal("expected false when yes flag is set")
		}
	})

	t.Run("returns true when stdin is a terminal and yes flag is not set", func(t *testing.T) {
		io := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }
		if !p.IsInteractive() {
			t.Fatal("expected true when stdin is a terminal and yes flag is not set")
		}
	})
}

func TestSelect(t *testing.T) {
	choices := []Choice{
		{Label: "Keep", Key: "k", Description: "Keep the worktree"},
		{Label: "Remove", Key: "r", Description: "Remove the worktree"},
	}

	t.Run("returns default in non-interactive mode", func(t *testing.T) {
		var stderr bytes.Buffer
		io := New(strings.NewReader("r\n"), &bytes.Buffer{}, &stderr, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := io.Select("What to do?", choices, "k")
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
		io := New(strings.NewReader("r\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r, got %q", got)
		}
	})

	t.Run("matches keys case-insensitively", func(t *testing.T) {
		io := New(strings.NewReader("R\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r, got %q", got)
		}
	})

	t.Run("uses default when user presses enter", func(t *testing.T) {
		io := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "k" {
			t.Fatalf("expected default key k, got %q", got)
		}
	})

	t.Run("retries on invalid input", func(t *testing.T) {
		io := New(strings.NewReader("x\nfoo\nr\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.Select("What to do?", choices, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r after retries, got %q", got)
		}
	})

	t.Run("returns error after too many retries", func(t *testing.T) {
		io := New(strings.NewReader("x\nx\nx\nx\nx\nx\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := io.Select("What to do?", choices, "k")
		if err == nil {
			t.Fatal("expected error after max retries")
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		io := New(&failingReader{}, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := io.Select("What to do?", choices, "k")
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})
}

func TestConfirmDefault(t *testing.T) {
	t.Run("returns default yes in non-interactive mode", func(t *testing.T) {
		io := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := io.ConfirmDefault("Proceed?", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true")
		}
	})

	t.Run("returns default no in non-interactive mode", func(t *testing.T) {
		io := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := io.ConfirmDefault("Proceed?", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Fatal("expected false")
		}
	})

	t.Run("accepts yes input", func(t *testing.T) {
		io := New(strings.NewReader("y\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.ConfirmDefault("Proceed?", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true")
		}
	})

	t.Run("accepts no input", func(t *testing.T) {
		io := New(strings.NewReader("n\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.ConfirmDefault("Proceed?", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Fatal("expected false")
		}
	})

	t.Run("empty input uses default yes", func(t *testing.T) {
		io := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.ConfirmDefault("Proceed?", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true")
		}
	})

	t.Run("retries on invalid input", func(t *testing.T) {
		io := New(strings.NewReader("x\ny\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.ConfirmDefault("Proceed?", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true")
		}
	})

	t.Run("returns error after too many retries", func(t *testing.T) {
		io := New(strings.NewReader("x\nx\nx\nx\nx\nx\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := io.ConfirmDefault("Proceed?", false)
		if err == nil {
			t.Fatal("expected error after max retries")
		}
	})
}

func TestInput(t *testing.T) {
	t.Run("returns default in non-interactive mode", func(t *testing.T) {
		io := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return false }

		got, err := io.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Fatalf("expected default main, got %q", got)
		}
	})

	t.Run("returns user input", func(t *testing.T) {
		io := New(strings.NewReader("feature\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "feature" {
			t.Fatalf("expected feature, got %q", got)
		}
	})

	t.Run("returns default on empty input", func(t *testing.T) {
		io := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		got, err := io.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Fatalf("expected default main, got %q", got)
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		io := New(&failingReader{}, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelNormal, false)
		p := io.(*printer)
		p.isTerminal = func(int) bool { return true }

		_, err := io.Input("Branch name:", "main")
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})
}

type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

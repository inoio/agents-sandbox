package prompt

import (
	"errors"
	"io"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
)

func TestIsInteractive(t *testing.T) {
	t.Run("returns false when stdin is not a terminal", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return false }
		AssumeYes = false

		if IsInteractive() {
			t.Fatal("expected false when stdin is not a terminal")
		}
	})

	t.Run("returns false when yes flag is set", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = true

		if IsInteractive() {
			t.Fatal("expected false when yes flag is set")
		}
	})

	t.Run("returns true when stdin is a terminal and yes flag is not set", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false

		if !IsInteractive() {
			t.Fatal("expected true when stdin is a terminal and yes flag is not set")
		}
	})
}

func TestSelect(t *testing.T) {
	choices := []Choice{
		{Label: "Keep", Key: "k", Description: "Keep the worktree"},
		{Label: "Remove", Key: "r", Description: "Remove the worktree"},
	}
	logger := output.NewPrinter(io.Discard, false)

	t.Run("returns default in non-interactive mode", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return false }
		AssumeYes = false
		stdin = strings.NewReader("r\n")

		got, err := Select("What to do?", choices, "k", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "k" {
			t.Fatalf("expected default key k, got %q", got)
		}
	})

	t.Run("returns matched key in interactive mode", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("r\n")

		got, err := Select("What to do?", choices, "k", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r, got %q", got)
		}
	})

	t.Run("matches keys case-insensitively", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("R\n")

		got, err := Select("What to do?", choices, "k", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r, got %q", got)
		}
	})

	t.Run("uses default when user presses enter", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("\n")

		got, err := Select("What to do?", choices, "k", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "k" {
			t.Fatalf("expected default key k, got %q", got)
		}
	})

	t.Run("retries on invalid input", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("x\nfoo\nr\n")

		got, err := Select("What to do?", choices, "k", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "r" {
			t.Fatalf("expected key r after retries, got %q", got)
		}
	})

	t.Run("returns error after too many retries", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("x\nx\nx\nx\nx\nx\n")

		_, err := Select("What to do?", choices, "k", logger)
		if err == nil {
			t.Fatal("expected error after max retries")
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = &failingReader{}

		_, err := Select("What to do?", choices, "k", logger)
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})
}

func TestConfirmDefault(t *testing.T) {
	logger := output.NewPrinter(io.Discard, false)

	t.Run("returns default yes in non-interactive mode", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return false }
		AssumeYes = false

		got, err := ConfirmDefault("Proceed?", true, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true")
		}
	})

	t.Run("returns default no in non-interactive mode", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return false }
		AssumeYes = false

		got, err := ConfirmDefault("Proceed?", false, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Fatal("expected false")
		}
	})

	t.Run("accepts yes input", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("y\n")

		got, err := ConfirmDefault("Proceed?", false, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true")
		}
	})

	t.Run("accepts no input", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("n\n")

		got, err := ConfirmDefault("Proceed?", true, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Fatal("expected false")
		}
	})

	t.Run("empty input uses default yes", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("\n")

		got, err := ConfirmDefault("Proceed?", true, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true")
		}
	})

	t.Run("empty input uses default no", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("\n")

		got, err := ConfirmDefault("Proceed?", false, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Fatal("expected false")
		}
	})

	t.Run("retries on invalid input", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("x\ny\n")

		got, err := ConfirmDefault("Proceed?", false, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected true")
		}
	})

	t.Run("returns error after too many retries", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("x\nx\nx\nx\nx\nx\n")

		_, err := ConfirmDefault("Proceed?", false, logger)
		if err == nil {
			t.Fatal("expected error after max retries")
		}
	})
}

func TestInput(t *testing.T) {
	logger := output.NewPrinter(io.Discard, false)

	t.Run("returns default in non-interactive mode", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return false }
		AssumeYes = false

		got, err := Input("Branch name:", "main", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Fatalf("expected default main, got %q", got)
		}
	})

	t.Run("returns user input", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("feature\n")

		got, err := Input("Branch name:", "main", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "feature" {
			t.Fatalf("expected feature, got %q", got)
		}
	})

	t.Run("returns default on empty input", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = strings.NewReader("\n")

		got, err := Input("Branch name:", "main", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Fatalf("expected default main, got %q", got)
		}
	})

	t.Run("returns error when reading fails", func(t *testing.T) {
		isTerminalFunc = func(int) bool { return true }
		AssumeYes = false
		stdin = &failingReader{}

		_, err := Input("Branch name:", "main", logger)
		if err == nil {
			t.Fatal("expected error when reading fails")
		}
	})
}

type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

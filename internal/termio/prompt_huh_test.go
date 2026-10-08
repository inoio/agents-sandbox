package termio

import (
	"bytes"
	"strings"
	"testing"
)

func huhTestChoices() []Choice {
	return []Choice{
		{Label: "Keep", Key: "k", Description: "Keep the worktree"},
		{Label: "Remove", Key: "r", Description: "Remove the worktree"},
	}
}

func TestParsePromptBackend(t *testing.T) {
	tests := []struct {
		name    string
		want    PromptBackend
		wantErr bool
	}{
		{name: "", want: PromptLine},
		{name: "line", want: PromptLine},
		{name: "huh", want: PromptHuh},
		{name: "HUH-ACCESSIBLE", want: PromptHuhAccessible},
		{name: "  huh  ", want: PromptHuh},
		{name: "invalid", want: PromptLine, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePromptBackend(tt.name)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParsePromptBackend(%q) err = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParsePromptBackend(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestHuhOptionsFoldsDescription(t *testing.T) {
	options := huhOptions(huhTestChoices())

	if len(options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(options))
	}
	if options[0].Key != "Keep - Keep the worktree" || options[0].Value != "k" {
		t.Errorf("unexpected first option: %+v", options[0])
	}
	if options[1].Key != "Remove - Remove the worktree" || options[1].Value != "r" {
		t.Errorf("unexpected second option: %+v", options[1])
	}
}

func TestSelectHuhAccessiblePicksByNumber(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(strings.NewReader("2\n"), &bytes.Buffer{}, &stderr, false, LevelInfo, false, false,
		WithPromptBackend(PromptHuhAccessible))
	ui.(*printer).isTerminal = func(int) bool { return true }

	got, err := ui.Select("What to do?", huhTestChoices(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "r" {
		t.Errorf("expected key r, got %q", got)
	}
	if !strings.Contains(stripANSICodes(stderr.String()), "2. Remove - Remove the worktree") {
		t.Errorf("expected numbered choices on stderr, got %q", stderr.String())
	}
}

func TestSelectHuhAccessibleEnterUsesDefault(t *testing.T) {
	ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false,
		WithPromptBackend(PromptHuhAccessible))
	ui.(*printer).isTerminal = func(int) bool { return true }

	got, err := ui.Select("What to do?", huhTestChoices(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "k" {
		t.Errorf("expected default key k, got %q", got)
	}
}

func TestSelectHuhNonInteractiveReturnsDefault(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(strings.NewReader("r\n"), &bytes.Buffer{}, &stderr, false, LevelInfo, false, false,
		WithPromptBackend(PromptHuh))
	ui.(*printer).isTerminal = func(int) bool { return false }

	got, err := ui.Select("What to do?", huhTestChoices(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "k" {
		t.Errorf("expected default key k, got %q", got)
	}
	if !strings.Contains(stderr.String(), "using default 'k'") {
		t.Errorf("expected default message on stderr, got %q", stderr.String())
	}
}

func TestInputHuhAccessible(t *testing.T) {
	t.Run("returns typed value", func(t *testing.T) {
		ui := New(strings.NewReader("feature\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false,
			WithPromptBackend(PromptHuhAccessible))
		ui.(*printer).isTerminal = func(int) bool { return true }

		got, err := ui.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "feature" {
			t.Errorf("expected feature, got %q", got)
		}
	})

	t.Run("returns default on empty input", func(t *testing.T) {
		ui := New(strings.NewReader("\n"), &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false,
			WithPromptBackend(PromptHuhAccessible))
		ui.(*printer).isTerminal = func(int) bool { return true }

		got, err := ui.Input("Branch name:", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Errorf("expected default main, got %q", got)
		}
	})
}

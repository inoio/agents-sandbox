package huhui

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func testChoices() []termio.Choice {
	return []termio.Choice{
		{Label: "Keep", Key: "k", Description: "Keep the worktree"},
		{Label: "Remove", Key: "r", Description: "Remove the worktree"},
	}
}

func TestChooseOptionsFoldsDescription(t *testing.T) {
	options := chooseOptions(testChoices())

	if len(options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(options))
	}
	if options[0].Key != "Keep - Keep the worktree" {
		t.Errorf("expected folded label, got %q", options[0].Key)
	}
	if options[0].Value != "k" {
		t.Errorf("expected value k, got %q", options[0].Value)
	}
	if options[1].Key != "Remove - Remove the worktree" || options[1].Value != "r" {
		t.Errorf("unexpected second option: %+v", options[1])
	}
}

func TestChooseOptionsWithoutDescription(t *testing.T) {
	options := chooseOptions([]termio.Choice{{Label: "Yes", Key: "y"}})

	if options[0].Key != "Yes" {
		t.Errorf("expected bare label, got %q", options[0].Key)
	}
}

func TestSelectNonInteractiveDelegates(t *testing.T) {
	var (
		gotPrompt  string
		gotDefault string
		gotChoices int
	)
	mock := &termio.Mock{IsInteractiveResult: false}
	mock.SelectFn = func(prompt string, choices []termio.Choice, defaultKey string) (string, error) {
		gotPrompt, gotChoices, gotDefault = prompt, len(choices), defaultKey
		return "delegated", nil
	}

	ui := New(mock, strings.NewReader("ignored\n"), ModeForm)
	got, err := ui.Select("pick", testChoices(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "delegated" {
		t.Errorf("expected delegation, got %q", got)
	}
	if gotPrompt != "pick" || gotChoices != 2 || gotDefault != "k" {
		t.Errorf("delegation args changed: prompt=%q choices=%d default=%q", gotPrompt, gotChoices, gotDefault)
	}
}

func TestSelectAccessiblePicksByNumber(t *testing.T) {
	mock := &termio.Mock{IsInteractiveResult: true}
	ui := New(mock, strings.NewReader("2\n"), ModeAccessible)

	got, err := ui.Select("pick", testChoices(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "r" {
		t.Errorf("expected key r, got %q", got)
	}
	if !strings.Contains(mock.StdErrBuffer.String(), "2. Remove - Remove the worktree") {
		t.Errorf("expected numbered choice on stderr, got %q", mock.StdErrBuffer.String())
	}
}

func TestSelectAccessibleEnterUsesDefault(t *testing.T) {
	mock := &termio.Mock{IsInteractiveResult: true}
	ui := New(mock, strings.NewReader("\n"), ModeAccessible)

	got, err := ui.Select("pick", testChoices(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "k" {
		t.Errorf("expected default key k, got %q", got)
	}
}

func TestInputNonInteractiveDelegates(t *testing.T) {
	mock := &termio.Mock{IsInteractiveResult: false}
	mock.InputFn = func(prompt, defaultValue string) (string, error) {
		return "delegated:" + defaultValue, nil
	}

	ui := New(mock, strings.NewReader("ignored\n"), ModeForm)
	got, err := ui.Input("branch", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "delegated:main" {
		t.Errorf("expected delegation, got %q", got)
	}
}

func TestInputAccessible(t *testing.T) {
	t.Run("returns typed value", func(t *testing.T) {
		mock := &termio.Mock{IsInteractiveResult: true}
		ui := New(mock, strings.NewReader("feature\n"), ModeAccessible)

		got, err := ui.Input("branch", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "feature" {
			t.Errorf("expected feature, got %q", got)
		}
	})

	t.Run("returns default on empty", func(t *testing.T) {
		mock := &termio.Mock{IsInteractiveResult: true}
		ui := New(mock, strings.NewReader("\n"), ModeAccessible)

		got, err := ui.Input("branch", "main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "main" {
			t.Errorf("expected default main, got %q", got)
		}
	})
}

package volume

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

func TestFromKeyMapsValidKeys(t *testing.T) {
	tests := []struct {
		key  string
		want VolumeAction
	}{
		{"k", ActionKeep},
		{"m", ActionMigrate},
		{"r", ActionReset},
		{"q", ActionQuit},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := FromKey(tt.key)
			if err != nil {
				t.Errorf("FromKey(%q) returned error: %v", tt.key, err)
			}
			if got != tt.want {
				t.Errorf("FromKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestFromKeyReturnsErrorForInvalidKey(t *testing.T) {
	_, err := FromKey("5")
	if err == nil {
		t.Errorf("FromKey(\"5\") returned nil error, want error")
	}
	_, err = FromKey("keep")
	if err == nil {
		t.Errorf("FromKey(\"keep\") returned nil error, want error")
	}
}

func TestFromKey(t *testing.T) {
	if a, err := FromKey("k"); err != nil || a != ActionKeep {
		t.Errorf("FromKey(k) = %v, %v", a, err)
	}
	if a, err := FromKey("m"); err != nil || a != ActionMigrate {
		t.Errorf("FromKey(m) = %v, %v", a, err)
	}
	if a, err := FromKey("r"); err != nil || a != ActionReset {
		t.Errorf("FromKey(r) = %v, %v", a, err)
	}
	if a, err := FromKey("q"); err != nil || a != ActionQuit {
		t.Errorf("FromKey(q) = %v, %v", a, err)
	}
	if _, err := FromKey("x"); err == nil {
		t.Error("FromKey(x) should error")
	}
}

// TestResolveHomeAction_SameDigestReturnsKeep pins that an unchanged image
// keeps the home volume without asking, even in an interactive session.
func TestResolveHomeAction_SameDigestReturnsKeep(t *testing.T) {
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			t.Error("prompted although the image is unchanged")
			return "r", nil
		},
	}
	vm := NewManager(docker.Get())
	action := vm.ResolveHomeAction(ui, "same-digest", "same-digest")
	if action != ActionKeep || len(ui.InfoCalls) != 0 {
		t.Errorf("action %v, info %v; want ActionKeep and nothing reported", action, ui.InfoCalls)
	}
}

func TestResolveHomeAction_DifferentDigestInNonInteractiveReturnsKeep(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	ui.IsInteractiveResult = false
	vm := NewManager(docker.Get())
	action := vm.ResolveHomeAction(&ui, "old", "new")
	if action != ActionKeep {
		t.Errorf("expected ActionKeep in non-interactive mode, got %v", action)
	}
}

func TestResolveHomeAction_DifferentDigestInInteractivePrompt(t *testing.T) {
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, choices []termio.Choice, _ string) (string, error) {
			if !strings.Contains(prompt, "Docker image changed") {
				return "", fmt.Errorf("unexpected prompt: %q", prompt)
			}
			if len(choices) != 4 {
				return "", fmt.Errorf("expected 4 choices, got %d", len(choices))
			}
			return "m", nil
		},
	}
	vm := NewManager(docker.Get())
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != ActionMigrate {
		t.Errorf("expected ActionMigrate, got %v", action)
	}
}

func TestResolveHomeAction_NonInteractiveExplains(t *testing.T) {
	vm := NewManager(docker.Get())
	ui := &termiotest.Mock{IsInteractiveResult: false}
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != ActionKeep {
		t.Fatalf("expected ActionKeep, got %v", action)
	}
	if len(ui.InfoCalls) == 0 || !strings.Contains(ui.InfoCalls[0], "image changed") ||
		!strings.Contains(ui.InfoCalls[0], "keeping") {
		t.Errorf("expected an explanatory keep message, got %v", ui.InfoCalls)
	}
}

func TestResolveHomeAction_ActionQuitReturnsQuit(t *testing.T) {
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "q", nil
		},
	}
	vm := NewManager(docker.Get())
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != ActionQuit {
		t.Errorf("expected ActionQuit, got %v", action)
	}
}

func TestResolveHomeAction_PromptFails_ReturnsKeep(t *testing.T) {
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("select failed")
		},
	}
	vm := NewManager(docker.Get())
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != ActionKeep {
		t.Errorf("expected ActionKeep on prompt failure, got %v", action)
	}
	if len(ui.WarnCalls) != 1 {
		t.Errorf("expected 1 warning on prompt failure, got %d", len(ui.WarnCalls))
	}
}

func TestActionLabel(t *testing.T) {
	if got := actionLabel(ActionReset); got != "reset" {
		t.Errorf("actionLabel(reset) = %q, want reset", got)
	}
	if got := actionLabel(ActionMigrate); got != "migrate" {
		t.Errorf("actionLabel(migrate) = %q, want migrate", got)
	}
	if got := actionLabel(ActionKeep); got != "keep" {
		t.Errorf("actionLabel(keep) = %q, want keep", got)
	}
}

func TestInvalidKeyError(t *testing.T) {
	e := &invalidKeyError{key: "z"}
	if got := e.Error(); got != "invalid action key: z" {
		t.Errorf("invalidKeyError.Error() = %q, want %q", got, "invalid action key: z")
	}
}

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

// askPolicy shows the dialog and offers to remember the answer through
// remembered.
func askPolicy(remembered *[]string) ImageChangePolicy {
	return ImageChangePolicy{Answer: AnswerAsk, Remember: func(answer string) (string, error) {
		*remembered = append(*remembered, answer)
		return "/home/u/.config/agents-sandbox", nil
	}}
}

// TestResolveHomeAction_SameDigestReturnsKeep pins that an unchanged image
// keeps the home volume without asking, even under ask.
func TestResolveHomeAction_SameDigestReturnsKeep(t *testing.T) {
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			t.Error("prompted although the image is unchanged")
			return "r", nil
		},
	}
	vm := NewManager(docker.Get())
	action := vm.ResolveHomeAction(ui, "same-digest", "same-digest", askPolicy(new([]string)))
	if action != ActionKeep || len(ui.InfoCalls) != 0 {
		t.Errorf("action %v, info %v; want ActionKeep and nothing reported", action, ui.InfoCalls)
	}
}

// TestResolveHomeAction_ConfiguredAnswerNeverAsks pins #845: a configured answer
// applies without the dialog, named in one info line with its key.
func TestResolveHomeAction_ConfiguredAnswerNeverAsks(t *testing.T) {
	verbs := map[VolumeAction]string{
		ActionKeep:    "keeping",
		ActionMigrate: "migrating",
		ActionReset:   "resetting",
		ActionQuit:    "",
	}
	for answer, want := range map[string]VolumeAction{
		"": ActionKeep, "keep": ActionKeep, "migrate": ActionMigrate, "reset": ActionReset, "bogus": ActionKeep,
	} {
		ui := &termiotest.Mock{
			IsInteractiveResult: true,
			SelectFn: func(string, []termio.Choice, string) (string, error) {
				t.Errorf("%q: prompted although an answer is configured", answer)
				return "q", nil
			},
		}
		vm := NewManager(docker.Get())
		action := vm.ResolveHomeAction(ui, "old", "new", ImageChangePolicy{Answer: answer, Remember: nil})
		if action != want {
			t.Errorf("%q: action %v, want %v", answer, action, want)
		}
		if len(ui.InfoCalls) != 1 || !strings.Contains(ui.InfoCalls[0], "home-volume.on-image-change") ||
			!strings.Contains(ui.InfoCalls[0], actionLabel(want)) || !strings.Contains(ui.InfoCalls[0], verbs[want]) {
			t.Errorf("%q: info %v, want one line naming the answer and the key", answer, ui.InfoCalls)
		}
	}
}

func TestResolveHomeAction_AskInNonInteractiveKeeps(t *testing.T) {
	vm := NewManager(docker.Get())
	ui := &termiotest.Mock{IsInteractiveResult: false}
	action := vm.ResolveHomeAction(ui, "old", "new", askPolicy(new([]string)))
	if action != ActionKeep {
		t.Fatalf("expected ActionKeep, got %v", action)
	}
	if len(ui.InfoCalls) == 0 || !strings.Contains(ui.InfoCalls[0], "image changed") ||
		!strings.Contains(ui.InfoCalls[0], "keeping") {
		t.Errorf("expected an explanatory keep message, got %v", ui.InfoCalls)
	}
}

func TestResolveHomeAction_AskShowsTheDialogAndRemembersOnRequest(t *testing.T) {
	var remembered []string
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		ConfirmResult:       true,
		SelectFn: func(prompt string, choices []termio.Choice, defaultKey string) (string, error) {
			if !strings.Contains(prompt, "runner image changed") || len(choices) != 4 || defaultKey != "k" {
				return "", fmt.Errorf("unexpected dialog: %q %v %q", prompt, choices, defaultKey)
			}
			return "m", nil
		},
	}
	vm := NewManager(docker.Get())
	action := vm.ResolveHomeAction(ui, "old", "new", askPolicy(&remembered))
	if action != ActionMigrate {
		t.Errorf("action %v, want ActionMigrate", action)
	}
	if len(ui.ConfirmCalls) != 1 || !strings.Contains(ui.ConfirmCalls[0], "home-volume.on-image-change: migrate") {
		t.Errorf("confirm %v, want the offer to remember migrate", ui.ConfirmCalls)
	}
	if len(remembered) != 1 || remembered[0] != "migrate" {
		t.Errorf("remembered %v, want migrate", remembered)
	}
	if !strings.Contains(strings.Join(ui.InfoCalls, "\n"), "/home/u/.config/agents-sandbox") {
		t.Errorf("info %v, want where the answer was saved", ui.InfoCalls)
	}
}

func TestResolveHomeAction_AskWithoutRememberingWritesNothing(t *testing.T) {
	var remembered []string
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		ConfirmResult:       false,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "k", nil },
	}
	vm := NewManager(docker.Get())
	if action := vm.ResolveHomeAction(ui, "old", "new", askPolicy(&remembered)); action != ActionKeep {
		t.Errorf("action %v, want ActionKeep", action)
	}
	if len(remembered) != 0 {
		t.Errorf("remembered %v, want nothing", remembered)
	}
}

func TestResolveHomeAction_AskReportsAFailedRemember(t *testing.T) {
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		ConfirmResult:       true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "r", nil },
	}
	policy := ImageChangePolicy{Answer: AnswerAsk, Remember: func(string) (string, error) {
		return "", errors.New("write boom")
	}}
	vm := NewManager(docker.Get())
	if action := vm.ResolveHomeAction(ui, "old", "new", policy); action != ActionReset {
		t.Errorf("action %v, want ActionReset", action)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "write boom") {
		t.Errorf("warnings %v, want the failed write", ui.WarnCalls)
	}
}

func TestResolveHomeAction_AskOffersNoRememberForQuitOrWithoutAWriter(t *testing.T) {
	for name, tc := range map[string]struct {
		key    string
		policy ImageChangePolicy
		want   VolumeAction
	}{
		"quit":      {"q", askPolicy(new([]string)), ActionQuit},
		"no writer": {"k", ImageChangePolicy{Answer: AnswerAsk, Remember: nil}, ActionKeep},
	} {
		ui := &termiotest.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { return tc.key, nil },
		}
		vm := NewManager(docker.Get())
		if action := vm.ResolveHomeAction(ui, "old", "new", tc.policy); action != tc.want {
			t.Errorf("%s: action %v, want %v", name, action, tc.want)
		}
		if len(ui.ConfirmCalls) != 0 {
			t.Errorf("%s: confirm %v, want no offer to remember", name, ui.ConfirmCalls)
		}
	}
}

func TestResolveHomeAction_PromptFails_ReturnsKeep(t *testing.T) {
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		ConfirmResult:       true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("select failed")
		},
	}
	vm := NewManager(docker.Get())
	action := vm.ResolveHomeAction(ui, "old", "new", askPolicy(new([]string)))
	if action != ActionKeep {
		t.Errorf("expected ActionKeep on prompt failure, got %v", action)
	}
	if len(ui.WarnCalls) != 1 {
		t.Errorf("expected 1 warning on prompt failure, got %d", len(ui.WarnCalls))
	}
	if len(ui.ConfirmCalls) != 0 {
		t.Errorf("confirm %v, want no offer to remember after a failed dialog", ui.ConfirmCalls)
	}
}

func TestResolveHomeAction_AskRemembersNothingWhenTheConfirmFails(t *testing.T) {
	var remembered []string
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		ConfirmResult:       true,
		ConfirmErr:          errors.New("confirm boom"),
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "m", nil },
	}
	vm := NewManager(docker.Get())
	vm.ResolveHomeAction(ui, "old", "new", askPolicy(&remembered))
	if len(remembered) != 0 {
		t.Errorf("remembered %v after a failed confirm, want nothing", remembered)
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

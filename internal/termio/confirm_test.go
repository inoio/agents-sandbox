package termio

import (
	"errors"
	"testing"
)

func TestConfirmActionAssumesYes(t *testing.T) {
	ui := &Mock{}
	ui.SetAssumeYes(true)
	if err := ConfirmAction(ui, "Remove it?"); err != nil {
		t.Fatalf("ConfirmAction(assume-yes) = %v, want nil", err)
	}
	if len(ui.ConfirmCalls) != 0 {
		t.Errorf("assume-yes must not prompt, got %v", ui.ConfirmCalls)
	}
}

func TestConfirmActionNonInteractiveFails(t *testing.T) {
	ui := &Mock{}
	err := ConfirmAction(ui, "Remove it?")
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("ConfirmAction(non-interactive) = %v, want ErrConfirmationRequired", err)
	}
}

func TestConfirmActionDeclined(t *testing.T) {
	ui := &Mock{IsInteractiveResult: true, ConfirmResult: false}
	if err := ConfirmAction(ui, "Remove it?"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("ConfirmAction(declined) = %v, want ErrCancelled", err)
	}
}

func TestConfirmActionAccepted(t *testing.T) {
	ui := &Mock{IsInteractiveResult: true, ConfirmResult: true}
	if err := ConfirmAction(ui, "Remove it?"); err != nil {
		t.Fatalf("ConfirmAction(accepted) = %v, want nil", err)
	}
}

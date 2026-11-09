package termio_test

import (
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

func TestConfirmActionHonoursAssumeYesInteractivityAndTheAnswer(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ui         *termiotest.Mock
		wantErr    error
		wantPrompt bool
	}{
		{
			name: "assume yes skips the prompt",
			ui: func() *termiotest.Mock {
				m := &termiotest.Mock{}
				m.SetAssumeYes(true)
				return m
			}(),
		},
		{name: "non-interactive requires confirmation", ui: &termiotest.Mock{}, wantErr: termio.ErrConfirmationRequired},
		{
			name: "declined cancels", ui: &termiotest.Mock{IsInteractiveResult: true, ConfirmResult: false},
			wantErr: termio.ErrCancelled, wantPrompt: true,
		},
		{name: "accepted proceeds", ui: &termiotest.Mock{IsInteractiveResult: true, ConfirmResult: true}, wantPrompt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := termio.ConfirmAction(tc.ui, "Remove it?")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ConfirmAction = %v, want %v", err, tc.wantErr)
			}
			if prompted := len(tc.ui.ConfirmCalls) > 0; prompted != tc.wantPrompt {
				t.Errorf("prompted = %v (%v), want %v", prompted, tc.ui.ConfirmCalls, tc.wantPrompt)
			}
		})
	}
}

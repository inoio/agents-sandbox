package cliout

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestRenderPruneReportModes(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		wantHuman bool
		wantOut   bool
	}{
		{"table calls human renderer", ModeTable, true, false},
		{"json writes value", ModeJSON, false, true},
		{"quiet writes nothing", ModeIdentifiers, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := &termio.Mock{}
			humanCalled := false
			RenderPruneReport(ui, tt.mode, func() { humanCalled = true }, map[string]bool{"dry_run": true})
			if humanCalled != tt.wantHuman {
				t.Errorf("human called = %v, want %v", humanCalled, tt.wantHuman)
			}
			if (len(ui.OutCalls) > 0) != tt.wantOut {
				t.Errorf("OutCalls = %v, wantOut %v", ui.OutCalls, tt.wantOut)
			}
		})
	}
}

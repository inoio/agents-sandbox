package cliout

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestResolveMode(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		quiet     bool
		wantMode  Mode
		wantError string
	}{
		{name: "default table", format: "", quiet: false, wantMode: ModeTable},
		{name: "quiet identifiers", format: "", quiet: true, wantMode: ModeIdentifiers},
		{name: "json", format: FormatJSON, quiet: false, wantMode: ModeJSON},
		{name: "json and quiet conflict", format: FormatJSON, quiet: true, wantError: "mutually exclusive"},
		{name: "unsupported format", format: "yaml", quiet: false, wantError: "unsupported format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, err := ResolveMode(tt.format, tt.quiet)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("ResolveMode(%q, %v) error = %v, want containing %q",
						tt.format, tt.quiet, err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveMode(%q, %v) unexpected error: %v", tt.format, tt.quiet, err)
			}
			if mode != tt.wantMode {
				t.Errorf("ResolveMode(%q, %v) = %v, want %v", tt.format, tt.quiet, mode, tt.wantMode)
			}
		})
	}
}

func TestWriteJSONIndents(t *testing.T) {
	ui := &termio.Mock{}
	if err := WriteJSON(ui, map[string]int{"a": 1}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if len(ui.OutCalls) != 1 || ui.OutCalls[0] != "{\n  \"a\": 1\n}" {
		t.Errorf("WriteJSON output = %q", ui.OutCalls)
	}
}

func TestWriteJSONMarshalError(t *testing.T) {
	ui := &termio.Mock{}
	if err := WriteJSON(ui, func() {}); err == nil {
		t.Fatal("expected a marshal error for a non-marshalable value")
	}
}

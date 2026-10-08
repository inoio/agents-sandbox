package main

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestPromptBackendFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want termio.PromptBackend
	}{
		{name: "unset defaults to line", env: "", want: termio.PromptLine},
		{name: "line", env: "line", want: termio.PromptLine},
		{name: "huh", env: "huh", want: termio.PromptHuh},
		{name: "huh-accessible", env: "huh-accessible", want: termio.PromptHuhAccessible},
		{name: "invalid falls back to line", env: "bogus", want: termio.PromptLine},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENCODE_SANDBOX_PROMPT", tt.env)
			if got := promptBackend(); got != tt.want {
				t.Errorf("promptBackend() = %v, want %v", got, tt.want)
			}
		})
	}
}

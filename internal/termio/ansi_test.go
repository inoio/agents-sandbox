package termio

import (
	"strings"
	"testing"
)

func TestStripANSICodes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "running", "running"},
		{"green bold", "\x1b[1;32mrunning\x1b[0m", "running"},
		{"dim", "\x1b[2mstopped\x1b[0m", "stopped"},
		{"yellow bold", "\x1b[1;33mdraining\x1b[0m", "draining"},
		{"red bold", "\x1b[1;31mcrashed\x1b[0m", "crashed"},
		{"mixed", "a\x1b[1;32mb\x1b[0mc", "abc"},
		{"no escapes", "hello world", "hello world"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripANSICodes(tc.in); got != tc.want {
				t.Errorf("stripANSICodes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStyleStatus(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   string
	}{
		{name: "running green bold", status: StatusRunning, want: "\x1b[1;32mrunning\x1b[0m"},
		{name: "stopped dim", status: StatusStopped, want: "\x1b[2mstopped\x1b[0m"},
		{name: "created dim", status: StatusCreated, want: "\x1b[2mcreated\x1b[0m"},
		{name: "starting yellow bold", status: StatusStarting, want: "\x1b[1;33mstarting\x1b[0m"},
		{name: "paused yellow bold", status: StatusPaused, want: "\x1b[1;33mpaused\x1b[0m"},
		{name: "draining yellow bold", status: StatusDraining, want: "\x1b[1;33mdraining\x1b[0m"},
		{name: "crashed red bold", status: StatusCrashed, want: "\x1b[1;31mcrashed\x1b[0m"},
		{name: "unknown plain", status: "rebooting", want: "rebooting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StyleStatus(tc.status); got != tc.want {
				t.Errorf("StyleStatus(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
	// Styled status strips back to the plain text.
	if got := stripANSICodes(StyleStatus(StatusRunning)); got != "running" {
		t.Errorf("strip(StyleStatus(running)) = %q, want running", got)
	}
}

func TestStyleStatusStripsToPlainText(t *testing.T) {
	for _, s := range []string{StatusRunning, StatusStopped, StatusStarting, StatusCrashed} {
		if got := stripANSICodes(StyleStatus(s)); got != s {
			t.Errorf("strip(StyleStatus(%q)) = %q, want %q", s, got, s)
		}
	}
}

func TestStyleStatusMarkers(t *testing.T) {
	green := StyleStatus(StatusRunning)
	if !strings.HasPrefix(green, "\x1b[1;32m") {
		t.Errorf("running should use green bold, got %q", green)
	}
	dim := StyleStatus(StatusStopped)
	if !strings.HasPrefix(dim, "\x1b[2m") {
		t.Errorf("stopped should use dim, got %q", dim)
	}
}

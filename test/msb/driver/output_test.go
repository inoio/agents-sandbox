package driver

import (
	"strings"
	"testing"
)

// TestSawDaemonTimeout pins that the shared marker helper fires on the
// launcher's real daemon-timeout phrase and stays quiet on unrelated output.
func TestSawDaemonTimeout(t *testing.T) {
	launcherPhrase := "opencode2 daemon did not become ready within 1m0s: 401 unauthorized"
	if !strings.Contains(launcherPhrase, daemonTimeoutMarker) {
		t.Fatalf("test phrase %q does not carry the launcher's marker %q", launcherPhrase, daemonTimeoutMarker)
	}
	if !SawDaemonTimeout([]byte("launcher output\n" + launcherPhrase)) {
		t.Error("SawDaemonTimeout = false, want true for the launcher's daemon-timeout phrase")
	}
	if SawDaemonTimeout([]byte("launcher output\nno daemon timeout here")) {
		t.Error("SawDaemonTimeout = true, want false for unrelated output")
	}
}

func TestStripANSI(t *testing.T) {
	if got := StripANSI([]byte("\x1b[31mred\x1b[0m")); got != "red" {
		t.Errorf("StripANSI = %q, want %q", got, "red")
	}
	// A sequence truncated by the end of the buffer is dropped too.
	if got := StripANSI([]byte("red\x1b[3")); got != "red" {
		t.Errorf("StripANSI = %q, want %q for a truncated sequence", got, "red")
	}
}

func TestTUIStarted(t *testing.T) {
	if !TUIStarted([]byte("launcher output\n\x1b[?1049htui")) {
		t.Error("TUIStarted = false, want true for alternate-screen enter sequence")
	}
	if TUIStarted([]byte("launcher output\nno tui yet")) {
		t.Error("TUIStarted = true, want false without the sequence")
	}
}

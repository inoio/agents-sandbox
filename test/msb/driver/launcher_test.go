package driver

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

// TestQuitIntervalKeepsPairsRapidAndWaitsBetweenThem pins the quit tap cadence:
// the two presses of a pair stay one QuitPressInterval apart (so a repeated-key
// exit chains), and every even press ends a pair and is followed by the longer
// quitRetryInterval, which gives a slow teardown room to finish before the next
// press.
func TestQuitIntervalKeepsPairsRapidAndWaitsBetweenThem(t *testing.T) {
	for _, test := range []struct {
		presses int
		want    time.Duration
	}{
		{presses: 1, want: QuitPressInterval},
		{presses: 2, want: quitRetryInterval},
		{presses: 3, want: QuitPressInterval},
		{presses: 4, want: quitRetryInterval},
	} {
		if got := quitIntervalAfterPress(test.presses); got != test.want {
			t.Errorf("quitIntervalAfterPress(%d) = %s, want %s", test.presses, got, test.want)
		}
	}
	// The pair must stay inside qwen-code's measured chaining window (~1.2s).
	if QuitPressInterval >= quitRetryInterval {
		t.Errorf("QuitPressInterval %s is not shorter than quitRetryInterval %s", QuitPressInterval, quitRetryInterval)
	}
}

// TestExitSaveTimeoutExceedsQuitTimeout pins the invariant the learn-exit wait
// depends on: answering the exit prompt runs the analysis/save path before the
// process exits, so the post-answer budget must be strictly larger than the
// quit budget or a successful run would be reported as a timeout.
func TestExitSaveTimeoutExceedsQuitTimeout(t *testing.T) {
	if ExitSaveTimeout <= QuitTimeout {
		t.Errorf("ExitSaveTimeout %s must exceed QuitTimeout %s", ExitSaveTimeout, QuitTimeout)
	}
}

// TestQuitNamesTheKeyWhenTheWriteStalls pins that a write failure in the quit
// loop reports the key it was pressing, so a stalled pty fails with the step
// named rather than the bare write timeout.
func TestQuitNamesTheKeyWhenTheWriteStalls(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.Close()

	// The quit loop pads while the pty is raw; cooked would drop input instead.
	if _, err := term.MakeRaw(ses.fd); err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	fillStalledPTY(t, ses)

	_, err := Quit(ses, Spec{QuitKey: []byte("q")})
	if err == nil {
		t.Fatal("Quit = nil error, want a stalled-write error")
	}
	if !strings.Contains(err.Error(), "0x71") {
		t.Errorf("Quit error %q does not name the quit key", err)
	}
}

package driver

import (
	"testing"
	"time"
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

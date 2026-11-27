package journeys

import (
	"errors"
	"testing"
	"time"
)

func TestPollUntilEndsOnDoneFailureOrTimeout(t *testing.T) {
	calls := 0
	if err := pollUntil(time.Second, time.Millisecond, func() (bool, error) {
		calls++
		return calls == 3, nil
	}, func() error { return errors.New("timed out") }); err != nil || calls != 3 {
		t.Errorf("done on the third check: err = %v after %d checks", err, calls)
	}

	failure := errors.New("the check failed")
	if err := pollUntil(time.Second, time.Millisecond, func() (bool, error) { return false, failure },
		func() error { return errors.New("timed out") }); !errors.Is(err, failure) {
		t.Errorf("a failing check: err = %v, want its own error", err)
	}

	checked := false
	timedOut := errors.New("timed out")
	if err := pollUntil(0, time.Millisecond, func() (bool, error) {
		checked = true
		return false, nil
	}, func() error { return timedOut }); !errors.Is(err, timedOut) || !checked {
		t.Errorf("an exhausted budget: err = %v, checked = %v; want one check and the timeout error", err, checked)
	}
}

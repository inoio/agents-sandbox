package journeys

import (
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// scaled is budget stretched by the suite's timeout factor for a slow host
// (driver.SmokeTimeoutFactor). Every wait a step bounds goes through it.
func scaled(budget time.Duration) time.Duration {
	return driver.ScaleTimeout(budget, driver.SmokeTimeoutFactor())
}

// pollUntil calls check every interval until it reports done or fails. When
// budget elapses first, it returns timedOut's error, which can describe the
// state the wait ended in. check runs at least once.
func pollUntil(budget, interval time.Duration, check func() (bool, error), timedOut func() error) error {
	deadline := time.Now().Add(budget)
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		if !time.Now().Before(deadline) {
			return timedOut()
		}
		//testenv:allow the poll cadence of a step waiting on a live VM or host stub
		time.Sleep(interval)
	}
}

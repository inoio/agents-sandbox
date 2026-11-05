package vm

import (
	"testing"
)

func TestExitError(t *testing.T) {
	e := &ExitError{Code: 42}
	if got := e.Error(); got != "exit code 42" {
		t.Errorf("ExitError.Error() = %q, want %q", got, "exit code 42")
	}
}

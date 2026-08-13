package session

import "fmt"

// autoFlag is the CLI flag that triggers auto-reap behavior.
const autoFlag = "--auto"

// ExitError is a non-zero exit code from a child process.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

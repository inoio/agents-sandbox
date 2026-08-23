package vm

import "fmt"

// ExitError is a non-zero exit code from a child process.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

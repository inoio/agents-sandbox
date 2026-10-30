package state

import (
	"errors"
	"fmt"
	"testing"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
)

// TestWriteInstalled pins the contract callers rely on to avoid rolling back an
// installed write: it recognizes the atomicwrite marker through the error
// wrapping the state writer adds.
func TestWriteInstalled(t *testing.T) {
	installed := fmt.Errorf("write state file: %w: sync dir boom", atomicwrite.ErrInstalled)
	if !WriteInstalled(installed) {
		t.Errorf("WriteInstalled(%v) = false, want true", installed)
	}
	if WriteInstalled(errors.New("plain boom")) {
		t.Error("WriteInstalled(plain error) = true, want false")
	}
}

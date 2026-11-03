package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestValidateWorktreeBaseExecError covers the sb.Exec failure branch of
// validateWorktreeBase.
func TestValidateWorktreeBaseExecError(t *testing.T) {
	sb := &msb.MockSandbox{ExecErr: errors.New("exec failed")}
	if err := validateWorktreeBase(context.Background(), sb, "/w/feat", "main"); err == nil {
		t.Error("expected error when the base-check exec fails")
	}
}

// failingStateDirConfigPaths overrides only the state directory, delegating
// every other path to the (nil) embedded interface. It is used to force
// read/write errors on the per-project state file by pointing UserStateDir at a
// regular file, so the parent "directory" cannot be created or read.
type failingStateDirConfigPaths struct {
	configpaths.ConfigPaths

	stateDir string
}

func (f failingStateDirConfigPaths) UserStateDir() string { return f.stateDir }

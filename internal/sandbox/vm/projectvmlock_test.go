package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/hostlock"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// TestProjectVMLockSurvivesStateRemoval is the #330 regression: the project's
// VM-creation lock lives under state's never-deleted locks tree (not the
// removable key dir), so a prune's state removal cannot unlink the lock a
// concurrent creation holds and let a second invocation create a duplicate VM.
func TestProjectVMLockSurvivesStateRemoval(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "myproj", Agent: "opencode"}

	lockDir := state.ProjectVMLockDir(k)
	if strings.HasPrefix(lockDir, state.KeyDir(k)) {
		t.Fatal("project-VM lock must not live in the removable key dir")
	}

	release, err := hostlock.AcquireExclusive(lockDir, "ensure-vm.lock")
	if err != nil {
		t.Fatalf("acquire project VM lock: %v", err)
	}
	defer release()

	if err := state.RemoveHomeStateIf(k, func(*state.HomeState) bool { return true }); err != nil {
		t.Fatalf("RemoveHomeStateIf: %v", err)
	}

	lockPath := filepath.Join(lockDir, "ensure-vm.lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("project-VM lock must survive state removal: %v", err)
	}

	probe, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock for probe: %v", err)
	}
	defer probe.Close()
	contentionErr := hostlock.ExclusiveNonBlocking(probe)
	if contentionErr == nil {
		t.Fatal("held project-VM lock must still contend after state removal")
	}
	// Only a lock-held error proves contention; an unrelated flock failure would
	// let the regression pass without another invocation having to wait.
	if !hostlock.IsContention(contentionErr) {
		t.Fatalf("probe must fail because the lock is held, got: %v", contentionErr)
	}
}

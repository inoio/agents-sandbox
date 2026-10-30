package vm

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestStopKillProjectVMAdoptionFailureFailsClosed pins the fail-closed adoption
// wiring on stop/kill: an unreadable legacy state aborts before the VM lookup,
// so a first post-upgrade stop cannot act on a half-understood identity.
func TestStopKillProjectVMAdoptionFailureFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(newOriginClone(t))
	identity := mustIdentity(t, ".")
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must have distinct repo and checkout slugs")
	}

	legacyDir := state.KeyDir(state.Key{Slug: identity.RepoSlug, Agent: "opencode"})
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy state dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, "state.yaml", `": [`)

	msb.WithMsbMock(t, &msb.MockMsbClient{})

	for _, action := range []struct {
		name string
		run  func(context.Context, bool, bool, termio.UI, string) error
	}{
		{"stop", StopProjectVM},
		{"kill", KillProjectVM},
	} {
		t.Run(action.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			err := action.run(context.Background(), false, false, &ui, "opencode")
			if err == nil || !strings.Contains(err.Error(), "check for a pending legacy identity") {
				t.Fatalf("error = %v, want the fail-closed predicate error", err)
			}
		})
	}
}

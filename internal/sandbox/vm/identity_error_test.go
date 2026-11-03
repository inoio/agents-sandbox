package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestStopKillProjectVMErrorsWhenIdentityUnresolvable pins #354 on the stop/kill
// paths: a cwd whose git dir cannot be resolved aborts before the legacy
// identity adoption could act on it.
func TestStopKillProjectVMErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))

	for _, action := range []struct {
		name string
		run  func(context.Context, configpaths.ConfigPaths, bool, bool, termio.UI, string) error
	}{
		{"stop", StopProjectVM},
		{"kill", KillProjectVM},
	} {
		t.Run(action.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			if err := action.run(context.Background(), configpaths.Get(), false, false, &ui, "opencode"); err == nil {
				t.Fatal("succeeded with an unresolvable identity, want an error")
			}
		})
	}
}

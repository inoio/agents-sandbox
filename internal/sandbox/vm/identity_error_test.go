package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestResolveSessionIdentityErrorsWhenIdentityUnresolvable pins #354 on the run
// path: a cwd whose git dir cannot be resolved aborts before any VM work.
func TestResolveSessionIdentityErrorsWhenIdentityUnresolvable(t *testing.T) {
	ui := termio.NewTestMock(t)
	_, _, _, err := resolveSessionIdentity(
		context.Background(), unresolvableIdentityDir(t), "opencode", false, &ui,
	)
	if err == nil {
		t.Fatal("resolveSessionIdentity succeeded with an unresolvable identity, want an error")
	}
}

// TestPreviewSandboxErrorsWhenIdentityUnresolvable pins #354 on the dry-run
// preview path, which resolves the cwd before it derives an identity.
func TestPreviewSandboxErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))

	ui := termio.NewTestMock(t)
	_, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui)
	if err == nil {
		t.Fatal("PreviewSandbox succeeded with an unresolvable identity, want an error")
	}
}

// TestStopKillProjectVMErrorsWhenIdentityUnresolvable pins #354 on the stop/kill
// paths: a cwd whose git dir cannot be resolved aborts before the legacy
// identity adoption could act on it.
func TestStopKillProjectVMErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))

	for _, action := range []struct {
		name string
		run  func(context.Context, bool, bool, termio.UI, string) error
	}{
		{"stop", StopProjectVM},
		{"kill", KillProjectVM},
	} {
		t.Run(action.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			if err := action.run(context.Background(), false, false, &ui, "opencode"); err == nil {
				t.Fatal("succeeded with an unresolvable identity, want an error")
			}
		})
	}
}

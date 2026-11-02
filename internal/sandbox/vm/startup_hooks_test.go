package vm

import (
	"context"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestRunStartupHooksDefaultsToDevUser verifies that a startup hook without an
// explicit user runs as the default sandbox user (dev), and that a script
// without a shebang falls back to /bin/sh. This is the path most users hit,
// unlike the root-run case covered by the integration flow.
func TestRunStartupHooksDefaultsToDevUser(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", Root: false},
	}, &ui)

	if sb.AttachUser != DefaultSandboxUser {
		t.Errorf("startup hook AttachWith user = %q, want default %q", sb.AttachUser, DefaultSandboxUser)
	}
	if home := sb.AttachEnv["HOME"]; home != "/home/dev" {
		t.Errorf("startup hook HOME = %q, want %q", home, "/home/dev")
	}
	if sb.AttachCmd != "/bin/sh" {
		t.Errorf("startup hook AttachWith cmd = %q, want fallback %q", sb.AttachCmd, "/bin/sh")
	}
	want := []string{"/home/dev/.vpn/connect.sh"}
	if !slices.Equal(sb.AttachArgs, want) {
		t.Errorf("startup hook AttachWith args = %v, want %v", sb.AttachArgs, want)
	}
}

// TestRunStartupHooksExecutesTargetDirectly verifies that a hook with a shebang
// is executed as the script path itself, so the VM kernel applies the shebang
// (issue #136).
func TestRunStartupHooksExecutesTargetDirectly(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.foo.sh", Source: "x", Root: false, HasShebang: true},
	}, &ui)

	if sb.AttachCmd != "/home/dev/.foo.sh" {
		t.Errorf("startup hook AttachWith cmd = %q, want the script path", sb.AttachCmd)
	}
	if len(sb.AttachArgs) != 0 {
		t.Errorf("startup hook AttachWith args = %v, want none", sb.AttachArgs)
	}
}

// TestRunStartupHooksRunsAsRoot verifies that a hook with Root set attaches as
// the root user, with root's home as HOME.
func TestRunStartupHooksRunsAsRoot(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", Root: true},
	}, &ui)

	if sb.AttachUser != "root" {
		t.Errorf("startup hook AttachWith user = %q, want %q", sb.AttachUser, "root")
	}
	if home := sb.AttachEnv["HOME"]; home != "/root" {
		t.Errorf("startup hook HOME = %q, want %q", home, "/root")
	}
}

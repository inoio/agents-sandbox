package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// runningCopyHandle returns a running project VM whose sandbox exposes fs.
func runningCopyHandle(fs *msbtest.TestFS) *msbtest.MockSandboxHandle {
	return &msbtest.MockSandboxHandle{
		Name_:     "vm",
		Status_:   msb.SandboxStatusRunning,
		ConnectSb: &msbtest.MockSandbox{FSValue_: fs},
	}
}

// The isolated tests below chdir to a non-repo temp dir, so the repo and
// checkout slugs coincide and AdoptLegacyIdentity has no legacy source to adopt
// as a side effect. The explicit TestCopyProjectFileAppliesAPendingLegacyIdentity
// uses an origin clone to assert the adoption deliberately.

func TestCopyProjectFileHostToVMCreatesNestedGuestParent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	fs := msbtest.NewTestFS(nil)
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	result, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/workspace/sub/patch.diff", &ui, "opencode")
	if err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	want := msbtest.MockCopyCall{Source: "/host/patch.diff", Destination: "/workspace/sub/patch.diff"}
	if got := fs.CopiedFromHost; len(got) != 1 || got[0] != want {
		t.Errorf("CopiedFromHost = %+v, want [%+v]", got, want)
	}
	if len(fs.Mkdirs) != 1 || fs.Mkdirs[0] != "/workspace/sub" {
		t.Errorf("Mkdirs = %v, want the guest parent /workspace/sub", fs.Mkdirs)
	}
	if result.Direction != CopyHostToVM || result.HostPath != "/host/patch.diff" ||
		result.GuestPath != "/workspace/sub/patch.diff" {
		t.Errorf("result = %+v", result)
	}
}

func TestCopyProjectFileVMToHostCopiesWithoutMkdir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	fs := msbtest.NewTestFS(nil)
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyVMToHost, "/host/out.diff", "/workspace/out.diff", &ui, "opencode"); err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	want := msbtest.MockCopyCall{Source: "/workspace/out.diff", Destination: "/host/out.diff"}
	if got := fs.CopiedToHost; len(got) != 1 || got[0] != want {
		t.Errorf("CopiedToHost = %+v, want [%+v]", got, want)
	}
	if len(fs.Mkdirs) != 0 {
		t.Errorf("Mkdirs = %v, want none for a VM-to-host copy", fs.Mkdirs)
	}
}

func TestCopyProjectFileRequiresARunningVM(t *testing.T) {
	for _, status := range []msb.SandboxStatus{
		msb.SandboxStatusCreated, msb.SandboxStatusStarting, msb.SandboxStatusStopped,
		msb.SandboxStatusCrashed, msb.SandboxStatusPaused,
	} {
		t.Run(string(status), func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			t.Chdir(t.TempDir())
			fs := msbtest.NewTestFS(nil)
			handle := runningCopyHandle(fs)
			handle.Status_ = status
			client := &msbtest.MockMsbClient{}
			client.SetGotSandbox(handle)
			ui := termiotest.NewTestMock(t)

			_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
				CopyHostToVM, "/host/patch.diff", "/workspace/patch.diff", &ui, "opencode")
			if err == nil || !strings.Contains(err.Error(), "not running") {
				t.Fatalf("error = %v, want a not-running refusal naming how to start", err)
			}
			if len(fs.CopiedFromHost) != 0 {
				t.Error("a non-running VM must not copy anything")
			}
		})
	}
}

func TestCopyProjectFileNoProjectVM(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	client := &msbtest.MockMsbClient{}
	client.SetGetSandboxErr(&msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"})
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/workspace/patch.diff", &ui, "opencode")
	if err == nil || !strings.Contains(err.Error(), "no project VM") {
		t.Fatalf("error = %v, want a no-project-VM refusal", err)
	}
}

func TestCopyProjectFilePropagatesCopyError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	cause := errors.New("copy boom")
	fs := msbtest.NewTestFS(nil)
	fs.CopyErr = cause
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/workspace/patch.diff", &ui, "opencode")
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want the copy failure %v", err, cause)
	}
}

func TestCopyDirectionString(t *testing.T) {
	if got := CopyHostToVM.String(); got != "host-to-vm" {
		t.Errorf("CopyHostToVM = %q, want host-to-vm", got)
	}
	if got := CopyVMToHost.String(); got != "vm-to-host" {
		t.Errorf("CopyVMToHost = %q, want vm-to-host", got)
	}
}

// TestCopyProjectFileAppliesAPendingLegacyIdentity pins the deliberate adoption:
// with a distinct repo slug carrying legacy state, cp applies the migration
// (apply=true) before resolving the checkout project VM, rather than copying
// from a half-understood identity.
func TestCopyProjectFileAppliesAPendingLegacyIdentity(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(newOriginClone(t))
	identity := mustIdentity(t, ".")
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must have distinct repo and checkout slugs")
	}
	legacyKey := state.Key{Slug: identity.RepoSlug, Agent: "opencode"}
	if err := seedHomeState(legacyKey, state.HomeState{HomeVolume: "home-legacy"}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	fs := msbtest.NewTestFS(nil)
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"}
	client := &msbtest.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, name string) (msb.SandboxHandle, error) {
		// Only the checkout VM exists; the legacy name must look absent so the
		// pending trigger comes from state, not from a name-agnostic stub.
		if name == ProjectVMName(checkoutKey) {
			return runningCopyHandle(fs), nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}
	ui := termiotest.NewTestMock(t)

	if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/workspace/patch.diff", &ui, "opencode"); err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	if len(fs.CopiedFromHost) != 1 {
		t.Errorf("CopiedFromHost = %+v, want the copy to run under the checkout identity", fs.CopiedFromHost)
	}
	if !statePresent(t, checkoutKey) {
		t.Error("the pending legacy state must be adopted onto the checkout")
	}
	if statePresent(t, legacyKey) {
		t.Error("the legacy state must be gone after the applied adoption")
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(c string) bool {
		return strings.Contains(c, "Migrating this checkout")
	}) {
		t.Errorf("WarnCalls = %v, want the applied-adoption notice", ui.WarnCalls)
	}
}

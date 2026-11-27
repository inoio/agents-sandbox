package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
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

// TestCopyProjectFileRefusesWorkspaceSelfCopy pins the data-loss guard: /workspace
// is the host checkout bind, so a host→VM copy whose guest destination maps back
// to the source host file would truncate that source (the SDK opens the guest
// sink with truncate before it reads). The copy is refused before any directory
// is created and leaves the source intact.
func TestCopyProjectFileRefusesWorkspaceSelfCopy(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	dir := t.TempDir()
	t.Chdir(dir)
	source := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	fs := msbtest.NewTestFS(nil)
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, source, "/workspace/big.bin", &ui, "opencode")
	if err == nil || !strings.Contains(err.Error(), "would overwrite its own source") {
		t.Fatalf("error = %v, want a self-copy refusal naming the overwrite", err)
	}
	if !strings.Contains(err.Error(), source) || !strings.Contains(err.Error(), "/workspace/big.bin") {
		t.Errorf("error = %v, want it to name both the source and the guest destination", err)
	}
	if len(fs.CopiedFromHost) != 0 {
		t.Errorf("CopiedFromHost = %+v, want none for a refused self-copy", fs.CopiedFromHost)
	}
	if len(fs.Mkdirs) != 0 {
		t.Errorf("Mkdirs = %v, want no directories created for a refused copy", fs.Mkdirs)
	}
	if got, readErr := os.ReadFile(source); readErr != nil || string(got) != "payload" {
		t.Errorf("source = %q, %v; want it left intact", got, readErr)
	}
}

// TestCopyProjectFileAllowsWorkspaceDestinationThatIsNotTheSource pins that the
// self-copy guard only fires for the same host file: a /workspace destination
// naming a different guest file proceeds.
func TestCopyProjectFileAllowsWorkspaceDestinationThatIsNotTheSource(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	dir := t.TempDir()
	t.Chdir(dir)
	source := filepath.Join(dir, "patch.diff")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	fs := msbtest.NewTestFS(nil)
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, source, "/workspace/other.diff", &ui, "opencode"); err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	want := msbtest.MockCopyCall{Source: source, Destination: "/workspace/other.diff"}
	if got := fs.CopiedFromHost; len(got) != 1 || got[0] != want {
		t.Errorf("CopiedFromHost = %+v, want [%+v]", got, want)
	}
}

// TestCopyProjectFileAllowsExistingDistinctWorkspaceDestination pins the
// both-stat-succeed-and-differ arm of the guard: an existing host file at the
// mapped destination that is not the source proceeds to the copy.
func TestCopyProjectFileAllowsExistingDistinctWorkspaceDestination(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	dir := t.TempDir()
	t.Chdir(dir)
	source := filepath.Join(dir, "src.diff")
	destination := filepath.Join(dir, "dst.diff")
	for _, path := range []string{source, destination} {
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	fs := msbtest.NewTestFS(nil)
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, source, "/workspace/dst.diff", &ui, "opencode"); err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	want := msbtest.MockCopyCall{Source: source, Destination: "/workspace/dst.diff"}
	if got := fs.CopiedFromHost; len(got) != 1 || got[0] != want {
		t.Errorf("CopiedFromHost = %+v, want [%+v]", got, want)
	}
}

func TestHostDestForGuest(t *testing.T) {
	binds := []hostBind{
		{guest: "/workspace", host: "/host/checkout"},
		{guest: "/workspace/sub", host: "/host/sub"},
		{guest: "/data", host: "/host/data"},
	}
	for _, tc := range []struct {
		name      string
		guestPath string
		want      string
		wantOK    bool
	}{
		{"the bind root itself", "/workspace", "/host/checkout", true},
		{"a file under the bind", "/workspace/f", "/host/checkout/f", true},
		{"a sibling prefix does not match", "/workspaceX/f", "", false},
		{"a cleaned path through the bind", "/workspace/../workspace/f", "/host/checkout/f", true},
		{"the longest matching mount wins", "/workspace/sub/f", "/host/sub/f", true},
		{"a second mount", "/data/f", "/host/data/f", true},
		{"a path outside every bind", "/etc/passwd", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := hostDestForGuest(tc.guestPath, binds)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("hostDestForGuest(%q) = (%q, %v), want (%q, %v)",
					tc.guestPath, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestCopyProjectFileRefusesConfiguredBindSelfCopy pins that the guard covers
// every host-backed read-write bind, not just /workspace: a configured rw bind
// whose guest destination aliases the source is refused.
func TestCopyProjectFileRefusesConfiguredBindSelfCopy(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	hostDir := t.TempDir()
	source := filepath.Join(hostDir, "big.bin")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	fs := msbtest.NewTestFS(nil)
	handle := runningCopyHandle(fs)
	handle.Mounts = map[string]msb.MountConfig{
		"/data": msb.Mount.Bind(hostDir, msb.MountOptions{}),
	}
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(handle)
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, source, "/data/big.bin", &ui, "opencode")
	if err == nil || !strings.Contains(err.Error(), "would overwrite its own source") {
		t.Fatalf("error = %v, want a self-copy refusal through the configured bind", err)
	}
	if len(fs.CopiedFromHost) != 0 || len(fs.Mkdirs) != 0 {
		t.Errorf("a refused copy must touch nothing: fromHost=%v mkdirs=%v", fs.CopiedFromHost, fs.Mkdirs)
	}
	if got, readErr := os.ReadFile(source); readErr != nil || string(got) != "payload" {
		t.Errorf("source = %q, %v; want it left intact", got, readErr)
	}
}

// TestCopyProjectFileAllowsReadonlyBindSelfCopy pins that a readonly bind is
// excluded from the guard: a readonly destination cannot truncate, so the copy
// proceeds.
func TestCopyProjectFileAllowsReadonlyBindSelfCopy(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	hostDir := t.TempDir()
	source := filepath.Join(hostDir, "big.bin")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	fs := msbtest.NewTestFS(nil)
	handle := runningCopyHandle(fs)
	handle.Mounts = map[string]msb.MountConfig{
		"/data": msb.Mount.Bind(hostDir, msb.MountOptions{Readonly: true}),
	}
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(handle)
	ui := termiotest.NewTestMock(t)

	if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, source, "/data/big.bin", &ui, "opencode"); err != nil {
		t.Fatalf("CopyProjectFile() error = %v, want a readonly bind to be excluded", err)
	}
	if len(fs.CopiedFromHost) != 1 {
		t.Errorf("CopiedFromHost = %+v, want the copy to proceed", fs.CopiedFromHost)
	}
}

// TestCopyProjectFileIgnoresNonHostBackedMounts pins that named and tmpfs mounts
// carry no host path and so are not part of the guard.
func TestCopyProjectFileIgnoresNonHostBackedMounts(t *testing.T) {
	for name, mount := range map[string]msb.MountConfig{
		"named": msb.Mount.Named("vol", msb.MountOptions{}),
		"tmpfs": msb.Mount.Tmpfs(msb.TmpfsOptions{}),
	} {
		t.Run(name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			t.Chdir(t.TempDir())
			fs := msbtest.NewTestFS(nil)
			handle := runningCopyHandle(fs)
			handle.Mounts = map[string]msb.MountConfig{"/data": mount}
			client := &msbtest.MockMsbClient{}
			client.SetGotSandbox(handle)
			ui := termiotest.NewTestMock(t)

			if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
				CopyHostToVM, "/host/big.bin", "/data/big.bin", &ui, "opencode"); err != nil {
				t.Fatalf("CopyProjectFile() error = %v, want the mount ignored", err)
			}
			if len(fs.CopiedFromHost) != 1 {
				t.Errorf("CopiedFromHost = %+v, want the copy to proceed", fs.CopiedFromHost)
			}
		})
	}
}

// TestCopyProjectFileWorkspaceGuardSurvivesMountReadError pins that a
// ConfigMounts failure does not block the copy: the static /workspace mapping
// still refuses an aliasing copy and still lets a non-aliasing one through.
func TestCopyProjectFileWorkspaceGuardSurvivesMountReadError(t *testing.T) {
	t.Run("self-copy still refused", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		dir := t.TempDir()
		t.Chdir(dir)
		source := filepath.Join(dir, "big.bin")
		if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
			t.Fatalf("seed source: %v", err)
		}
		fs := msbtest.NewTestFS(nil)
		handle := runningCopyHandle(fs)
		handle.MountsErr = errors.New("mount read boom")
		client := &msbtest.MockMsbClient{}
		client.SetGotSandbox(handle)
		ui := termiotest.NewTestMock(t)

		_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
			CopyHostToVM, source, "/workspace/big.bin", &ui, "opencode")
		if err == nil || !strings.Contains(err.Error(), "would overwrite its own source") {
			t.Fatalf("error = %v, want the static /workspace mapping to still refuse", err)
		}
		if len(fs.CopiedFromHost) != 0 {
			t.Errorf("CopiedFromHost = %+v, want none", fs.CopiedFromHost)
		}
	})

	t.Run("non-aliasing copy proceeds", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		dir := t.TempDir()
		t.Chdir(dir)
		source := filepath.Join(dir, "patch.diff")
		if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
			t.Fatalf("seed source: %v", err)
		}
		fs := msbtest.NewTestFS(nil)
		handle := runningCopyHandle(fs)
		handle.MountsErr = errors.New("mount read boom")
		client := &msbtest.MockMsbClient{}
		client.SetGotSandbox(handle)
		ui := termiotest.NewTestMock(t)

		if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
			CopyHostToVM, source, "/workspace/other.diff", &ui, "opencode"); err != nil {
			t.Fatalf("CopyProjectFile() error = %v", err)
		}
		if len(fs.CopiedFromHost) != 1 {
			t.Errorf("CopiedFromHost = %+v, want the copy to proceed", fs.CopiedFromHost)
		}
	})
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

// TestCopyProjectFileRejectsAnUnknownAgent pins that copy refuses an agent name
// that is not registered, before it resolves any identity.
func TestCopyProjectFileRejectsAnUnknownAgent(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	_, err := CopyProjectFile(context.Background(), configpaths.Get(), &msbtest.MockMsbClient{},
		CopyHostToVM, "/host/a", "/workspace/a", &ui, "no-such-agent")
	if err == nil || !strings.Contains(err.Error(), `unknown agent "no-such-agent"`) {
		t.Fatalf("error = %v, want the unknown agent refused", err)
	}
}

// TestCopyProjectFileErrorsWhenIdentityUnresolvable pins that a cwd whose git dir
// cannot be resolved aborts before the legacy identity adoption could act on it.
func TestCopyProjectFileErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))
	ui := termiotest.NewTestMock(t)
	_, err := CopyProjectFile(context.Background(), configpaths.Get(), &msbtest.MockMsbClient{},
		CopyHostToVM, "/host/a", "/workspace/a", &ui, "opencode")
	if err == nil {
		t.Fatal("succeeded with an unresolvable identity, want an error")
	}
}

// TestCopyProjectFileAdoptionFailureFailsClosed pins the fail-closed adoption
// wiring on copy: an unreadable legacy state aborts before the VM lookup, so a
// first post-upgrade copy cannot act on a half-understood identity.
func TestCopyProjectFileAdoptionFailureFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(newOriginClone(t))
	identity, resolveErr := git.ResolveIdentity(".")
	if resolveErr != nil {
		t.Fatalf("resolve identity: %v", resolveErr)
	}
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must have distinct repo and checkout slugs")
	}

	legacyDir := state.KeyDir(state.Key{Slug: identity.RepoSlug, Agent: "opencode"})
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy state dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, "state.yaml", `": [`)

	ui := termiotest.NewTestMock(t)
	_, err := CopyProjectFile(context.Background(), configpaths.Get(), &msbtest.MockMsbClient{},
		CopyHostToVM, "/host/a", "/workspace/a", &ui, "opencode")
	if err == nil || !strings.Contains(err.Error(), "check for a pending legacy identity") {
		t.Fatalf("error = %v, want the fail-closed predicate error", err)
	}
}

// TestCopyProjectFilePropagatesVMLookupError pins that a non-not-found lookup
// failure surfaces rather than being read as an absent VM.
func TestCopyProjectFilePropagatesVMLookupError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	cause := errors.New("lookup boom")
	client := &msbtest.MockMsbClient{}
	client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
		return nil, cause
	}
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/a", "/workspace/a", &ui, "opencode")
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want the lookup failure %v", err, cause)
	}
}

// TestCopyProjectFilePropagatesConnectError pins that a connect failure surfaces.
func TestCopyProjectFilePropagatesConnectError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	cause := errors.New("connect boom")
	handle := runningCopyHandle(msbtest.NewTestFS(nil))
	handle.ConnectErr = cause
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(handle)
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/a", "/workspace/a", &ui, "opencode")
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want the connect failure %v", err, cause)
	}
}

// TestCopyProjectFileVMToHostPropagatesCopyError pins the vm-to-host copy
// failure path (distinct from the host-to-vm one).
func TestCopyProjectFileVMToHostPropagatesCopyError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	cause := errors.New("copy out boom")
	fs := msbtest.NewTestFS(nil)
	fs.CopyErr = cause
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyVMToHost, "/host/out.diff", "/workspace/out.diff", &ui, "opencode")
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want the copy failure %v", err, cause)
	}
}

// TestCopyProjectFileHostToVMPropagatesMkdirError pins that a failed nested
// guest-parent creation surfaces before the copy.
func TestCopyProjectFileHostToVMPropagatesMkdirError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	cause := errors.New("mkdir boom")
	fs := msbtest.NewTestFS(nil)
	fs.MkdirErr = cause
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/a", "/workspace/sub/a", &ui, "opencode")
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want the mkdir failure %v", err, cause)
	}
}

// TestCopyProjectFileHostToVMCopiesOutsideTheWorkspaceBind pins that the
// self-copy guard only covers the /workspace bind: a guest destination outside
// it proceeds to the transfer.
func TestCopyProjectFileHostToVMCopiesOutsideTheWorkspaceBind(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	fs := msbtest.NewTestFS(nil)
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/tmp/out.diff", &ui, "opencode"); err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	want := msbtest.MockCopyCall{Source: "/host/patch.diff", Destination: "/tmp/out.diff"}
	if got := fs.CopiedFromHost; len(got) != 1 || got[0] != want {
		t.Errorf("CopiedFromHost = %+v, want [%+v]", got, want)
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

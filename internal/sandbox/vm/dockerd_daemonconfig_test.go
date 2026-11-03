package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// daemonJSONSandbox returns a MockSandbox whose guest has the given
// /etc/docker/daemon.json contents (omitted when content is empty) and reports
// fuse-overlayfs as unavailable, exercising the vfs fallback.
func daemonJSONSandbox(content string) *msb.MockSandbox {
	return daemonJSONSandboxWithFuse(content, false)
}

// daemonJSONSandboxWithFuse returns a MockSandbox with the given daemon.json
// contents whose guest probe reports the fuse-overlayfs binary and /dev/fuse as
// available or not.
func daemonJSONSandboxWithFuse(content string, fuseAvailable bool) *msb.MockSandbox {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	return &msb.MockSandbox{
		FSValue_: msb.NewTestFS(files, nil),
		ShellOut: map[string]msb.ShellResult{
			fuseOverlayfsProbeCmd: msb.NewTestResult(fuseAvailable, 0, "", "", nil),
		},
	}
}

func TestDockerdArgsDefaultsToVFSFlag(t *testing.T) {
	args, driver, err := dockerdArgs(context.Background(), daemonJSONSandbox(""))
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if driver != vfsStorageDriver {
		t.Errorf("driver = %q, want %q when fuse-overlayfs is unavailable", driver, vfsStorageDriver)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when daemon.json is absent; got %v", args)
	}
}

// TestDockerdArgsUsesFuseOverlayfsWhenAvailable guards the #489 default: when
// the image's daemon.json sets no driver and the guest provides the
// fuse-overlayfs binary and a usable /dev/fuse, dockerd is asked for
// fuse-overlayfs, whose copy-on-write layers beat vfs's full copy per layer.
func TestDockerdArgsUsesFuseOverlayfsWhenAvailable(t *testing.T) {
	args, driver, err := dockerdArgs(context.Background(), daemonJSONSandboxWithFuse("", true))
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if driver != fuseOverlayfsStorageDriver {
		t.Errorf("driver = %q, want %q when fuse-overlayfs is available", driver, fuseOverlayfsStorageDriver)
	}
	if !slices.Contains(args, "--storage-driver="+fuseOverlayfsStorageDriver) {
		t.Errorf("expected the fuse-overlayfs flag; got %v", args)
	}
}

// TestDockerdArgsAcceptsFuseOverlayfsFromDaemonJSON guards the #287 rule for the
// new driver: a daemon.json that already forces fuse-overlayfs is honored with
// no --storage-driver flag (dockerd rejects a directive set both ways).
func TestDockerdArgsAcceptsFuseOverlayfsFromDaemonJSON(t *testing.T) {
	args, driver, err := dockerdArgs(
		context.Background(),
		daemonJSONSandboxWithFuse(`{"storage-driver":"fuse-overlayfs"}`, false),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if driver != fuseOverlayfsStorageDriver {
		t.Errorf("driver = %q, want %q", driver, fuseOverlayfsStorageDriver)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--storage-driver") {
			t.Errorf("must not pass the flag when daemon.json already sets the driver; got %v", args)
		}
	}
}

// TestDockerdArgsFallsBackToVFSOnProbeError guards the failure mode: a probe
// transport error must not fail the VM start; selection falls back to vfs.
func TestDockerdArgsFallsBackToVFSOnProbeError(t *testing.T) {
	sb := &msb.MockSandbox{
		FSValue_: msb.NewTestFS(nil, nil),
		ShellErr: errors.New("probe boom"),
	}
	args, driver, err := dockerdArgs(context.Background(), sb)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if driver != vfsStorageDriver {
		t.Errorf("driver = %q, want %q after a probe error", driver, vfsStorageDriver)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs fallback flag; got %v", args)
	}
}

func TestDockerdArgsKeepsFlagForUnrelatedKeys(t *testing.T) {
	args, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"registry-mirrors":["https://mirror.example"]}`),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when daemon.json sets no storage driver; got %v", args)
	}
}

// TestDockerdArgsDropsFlagWhenDaemonJSONSetsVFS guards #287: when the image
// already forces vfs in daemon.json, passing --storage-driver=vfs as well makes
// dockerd refuse to start ("specified both as a flag and in the configuration
// file").
func TestDockerdArgsDropsFlagWhenDaemonJSONSetsVFS(t *testing.T) {
	args, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":"vfs"}`),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--storage-driver") {
			t.Errorf("must not pass the flag when daemon.json already sets vfs; got %v", args)
		}
	}
	if !slices.Contains(args, dockerdSocket) {
		t.Errorf("expected dockerd to still be started on its socket; got %v", args)
	}
}

func TestDockerdArgsFailsOnOtherStorageDriver(t *testing.T) {
	_, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":"overlay2"}`),
	)
	if err == nil {
		t.Fatal("expected an error when daemon.json requests a non-vfs driver")
	}
	if !strings.Contains(err.Error(), "overlay2") ||
		!strings.Contains(err.Error(), vfsStorageDriver) ||
		!strings.Contains(err.Error(), fuseOverlayfsStorageDriver) {
		t.Errorf("error should name the offending and both accepted drivers, got: %v", err)
	}
}

func TestDockerdArgsMalformedDaemonJSONFallsBackToFlag(t *testing.T) {
	args, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":`),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when daemon.json cannot be parsed; got %v", args)
	}
}

func TestDockerdArgsNullStorageDriverFallsBackToFlag(t *testing.T) {
	args, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":null}`),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when storage-driver is null; got %v", args)
	}
}

func TestDockerdArgsNonStringStorageDriverFails(t *testing.T) {
	_, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":42}`),
	)
	if err == nil {
		t.Fatal("expected an error for a non-string storage-driver value")
	}
}

func TestDockerdArgsReadErrorFails(t *testing.T) {
	fs := msb.NewTestFS(map[string][]byte{dockerdDaemonConfigPath: []byte(`{}`)}, nil)
	readErr := errors.New("read boom")
	fs.ReadErr = readErr
	sb := &msb.MockSandbox{FSValue_: fs}
	_, _, err := dockerdArgs(context.Background(), sb)
	if err == nil {
		t.Fatal("expected an error when daemon.json cannot be read")
	}
	if !errors.Is(err, readErr) {
		t.Errorf("error = %v, want it to wrap the read cause", err)
	}
}

// dockerdStartSandbox drives startDockerdIfPresent down its start path (the
// pre-restart readiness probe fails, the post-restart probe succeeds) and
// records every shell command. ExecDetached is delegated to the embedded
// MockSandbox.
type dockerdStartSandbox struct {
	msb.Sandbox

	calls      []string
	readyCalls int
}

func (s *dockerdStartSandbox) Shell(
	_ context.Context,
	command string,
	_ ...msbSdk.ExecOption,
) (msb.ShellResult, error) {
	s.calls = append(s.calls, command)
	switch command {
	case dockerdBinaryCheckCmd:
		return msb.NewTestResult(true, 0, "", "", nil), nil
	case dockerdReadyCmd:
		s.readyCalls++
		return msb.NewTestResult(s.readyCalls > 1, 0, "", "", nil), nil
	}
	return msb.NewTestResult(true, 0, "", "", nil), nil
}

func newDockerdStartSandbox(content string) (*dockerdStartSandbox, *[]msb.MockDetachedCall) {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	detachedCalls := &[]msb.MockDetachedCall{}
	mock := &msb.MockSandbox{
		FSValue_:          msb.NewTestFS(files, nil),
		ExecDetachedCalls: detachedCalls,
	}
	return &dockerdStartSandbox{Sandbox: mock}, detachedCalls
}

func TestStartDockerdUsesConfigDrivenArgsWhenDaemonJSONSetsVFS(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb, detached := newDockerdStartSandbox(`{"storage-driver":"vfs"}`)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	if len(*detached) != 1 {
		t.Fatalf("ExecDetached calls = %+v, want one dockerd launch", *detached)
	}
	for _, arg := range (*detached)[0].Args {
		if strings.HasPrefix(arg, "--storage-driver") {
			t.Errorf("must not pass --storage-driver when daemon.json sets vfs; got %v", (*detached)[0].Args)
		}
	}
}

func TestStartDockerdFailsOnOtherStorageDriver(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb, detached := newDockerdStartSandbox(`{"storage-driver":"overlay2"}`)

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected startDockerdIfPresent to fail for a non-vfs driver")
	}
	if !strings.Contains(err.Error(), "overlay2") {
		t.Errorf("error should name the offending driver, got: %v", err)
	}
	if len(*detached) != 0 {
		t.Errorf("dockerd must not be started when the driver conflicts, got %+v", *detached)
	}
}

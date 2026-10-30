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
// /etc/docker/daemon.json contents (omitted when content is empty).
func daemonJSONSandbox(content string) *msb.MockSandbox {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	return &msb.MockSandbox{FSValue_: msb.NewTestFS(files, nil)}
}

func TestDockerdArgsDefaultsToVFSFlag(t *testing.T) {
	args, err := dockerdArgs(context.Background(), daemonJSONSandbox(""))
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when daemon.json is absent; got %v", args)
	}
}

func TestDockerdArgsKeepsFlagForUnrelatedKeys(t *testing.T) {
	args, err := dockerdArgs(
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
	args, err := dockerdArgs(
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
	_, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":"overlay2"}`),
	)
	if err == nil {
		t.Fatal("expected an error when daemon.json requests a non-vfs driver")
	}
	if !strings.Contains(err.Error(), "overlay2") || !strings.Contains(err.Error(), vfsStorageDriver) {
		t.Errorf("error should name the offending and required drivers, got: %v", err)
	}
}

func TestDockerdArgsMalformedDaemonJSONFallsBackToFlag(t *testing.T) {
	args, err := dockerdArgs(
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
	args, err := dockerdArgs(
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
	_, err := dockerdArgs(
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
	_, err := dockerdArgs(context.Background(), sb)
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

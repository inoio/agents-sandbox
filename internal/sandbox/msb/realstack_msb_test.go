//go:build msb

package msb

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
)

// The msb-tagged tests drive the real SDK and host runtime through
// realMsbClient — the only place the adapter-over-SDK behavior actually runs.
// They are host-only (`make test-msb`) because they need the native library and
// the developer's real microsandbox state; the untagged unit tests cover the
// same adapters through the seam vars with fakes and never load the library.

// TestRealClientVolumeLifecycle round-trips a volume against the real runtime.
func TestRealClientVolumeLifecycle(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	client := &realMsbClient{}
	ctx := context.Background()
	name := "agents-sandbox-msb-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	created, err := client.CreateVolume(ctx, name, VolumeSpec{Kind: VolumeKindDir})
	if err != nil {
		t.Fatalf("CreateVolume(%q) error = %v", name, err)
	}
	if created == nil {
		t.Fatal("CreateVolume returned a nil handle")
	}
	t.Cleanup(func() { _ = client.RemoveVolume(context.Background(), name) })

	volumes, err := client.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
	if !volumeNamed(volumes, name) {
		t.Errorf("ListVolumes() did not include the created volume %q", name)
	}
	if _, err := client.GetVolume(ctx, name); err != nil {
		t.Errorf("GetVolume(%q) error = %v", name, err)
	}
	if err := client.RemoveVolume(ctx, name); err != nil {
		t.Errorf("RemoveVolume(%q) error = %v", name, err)
	}
}

// TestRealClientListsSandboxesAndImages drives the real list adapters against
// the host runtime.
func TestRealClientListsSandboxesAndImages(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	client := &realMsbClient{}
	ctx := context.Background()

	if _, err := client.ListSandboxes(ctx, nil); err != nil {
		t.Errorf("ListSandboxes() error = %v", err)
	}
	if _, err := client.ImageList(ctx); err != nil {
		t.Errorf("ImageList() error = %v", err)
	}
}

// The child-process environment: when childHomeEnv is set, the test binary is a
// child of TestRealClientSandboxLifecycle and TestRealClientConnectOrStartChild runs one
// ConnectOrStart against the parent's runtime home and sandbox.
const (
	childHomeEnv    = "AGENTS_SANDBOX_TEST_CHILD_MSB_HOME"
	childNameEnv    = "AGENTS_SANDBOX_TEST_CHILD_SANDBOX"
	childReadyLine  = "connect-or-start ready"
	childBootedLine = "connect-or-start booted="
)

// TestRealClientSandboxLifecycle drives the sandbox create and connect-or-start
// adapters against the real runtime and pins the semantics the project VM
// lifecycle relies on (specs/identity/sandbox-identity.md), so an SDK bump that changes
// them fails here instead of in a user's run:
//   - creating a taken name without replace fails with "already exists";
//   - of several processes calling ConnectOrStart on one stopped sandbox,
//     exactly one reports the boot and all converge on the same sandbox;
//   - a handle whose name was destroyed and recreated is rejected as replaced.
func TestRealClientSandboxLifecycle(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := realMsbClient{}
	name := "agents-sandbox-msb-lifecycle-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	createSpec := SandboxSpec{
		Image:       "alpine:3.20",
		Detached:    true,
		IdleTimeout: runtime.TestIdleTimeout,
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), runtime.DestroyTimeout)
		defer cleanupCancel()
		if handle, err := client.GetSandbox(cleanupCtx, name); err == nil {
			_ = handle.Destroy(cleanupCtx, LifecycleOptions{})
		}
	})

	if _, err := client.CreateSandbox(ctx, name, createSpec); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if _, err := client.CreateSandbox(ctx, name, createSpec); !IsSandboxAlreadyExists(err) {
		t.Fatalf("CreateSandbox on a taken name = %v, want an already-exists error", err)
	}

	original := handleNamed(ctx, t, client, name)
	if err := original.Stop(ctx, LifecycleOptions{}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if booted := connectOrStartFromProcesses(t, name, 3); booted != 1 {
		t.Fatalf("processes that booted the stopped sandbox = %d, want exactly 1", booted)
	}
	converged := handleNamed(ctx, t, client, name)
	if converged.ID() != original.ID() || converged.Status() != SandboxStatusRunning {
		t.Fatalf("sandbox after the race = %s (%s), want %s running",
			converged.ID(), converged.Status(), original.ID())
	}

	if err := original.Destroy(ctx, LifecycleOptions{}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := client.CreateSandbox(ctx, name, createSpec); err != nil {
		t.Fatalf("CreateSandbox after destroy: %v", err)
	}
	if sb, _, err := original.ConnectOrStart(ctx); !IsReplaced(err) || sb != nil {
		t.Fatalf("ConnectOrStart on a replaced handle = %v, %v; want a replaced error", sb, err)
	}
}

func handleNamed(ctx context.Context, t *testing.T, client realMsbClient, name string) SandboxHandle {
	t.Helper()
	handle, err := client.GetSandbox(ctx, name)
	if err != nil {
		t.Fatalf("GetSandbox(%q): %v", name, err)
	}
	return handle
}

// connectOrStartFromProcesses runs ConnectOrStart on the named sandbox from
// count child processes, as concurrent launcher invocations would, and returns
// how many of them booted it. Each child resolves its handle and reports ready;
// only once every child is ready does the parent release them together, so the
// calls overlap however long a child takes to get there.
func connectOrStartFromProcesses(t *testing.T, name string, count int) int {
	t.Helper()
	children := make([]*connectOrStartChild, count)
	for index := range children {
		children[index] = startConnectOrStartChild(t, name)
	}
	for index, child := range children {
		if !child.awaitReady() {
			t.Fatalf("child %d exited before it was ready:\n%s", index, child.finish())
		}
	}
	for _, child := range children {
		child.release()
	}
	booted := 0
	for index, child := range children {
		output := child.finish()
		switch {
		case strings.Contains(output, childBootedLine+"true"):
			booted++
		case !strings.Contains(output, childBootedLine+"false"):
			t.Errorf("child %d reported no connect-or-start outcome:\n%s", index, output)
		}
	}
	return booted
}

// connectOrStartChild is one running TestRealClientConnectOrStartChild process:
// its stdin is the release signal, its stdout the ready and outcome lines.
type connectOrStartChild struct {
	command *exec.Cmd
	release func()
	lines   *bufio.Scanner
	output  strings.Builder
}

func startConnectOrStartChild(t *testing.T, name string) *connectOrStartChild {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestRealClientConnectOrStartChild$", "-test.v")
	command.Env = append(os.Environ(), childHomeEnv+"="+runtime.IsolatedRuntimeHome(), childNameEnv+"="+name)
	child := &connectOrStartChild{command: command}
	command.Stderr = &child.output
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("child stdin: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("child stdout: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	child.release = func() { _ = stdin.Close() }
	child.lines = bufio.NewScanner(stdout)
	return child
}

// awaitReady reads the child's output up to its ready line and reports whether
// the child got there.
func (c *connectOrStartChild) awaitReady() bool {
	for c.lines.Scan() {
		c.output.WriteString(c.lines.Text() + "\n")
		if c.lines.Text() == childReadyLine {
			return true
		}
	}
	return false
}

// finish reads the child's remaining output, waits for it to exit, and returns
// everything it wrote.
func (c *connectOrStartChild) finish() string {
	c.release()
	for c.lines.Scan() {
		c.output.WriteString(c.lines.Text() + "\n")
	}
	if err := c.command.Wait(); err != nil {
		c.output.WriteString("exit: " + err.Error() + "\n")
	}
	return c.output.String()
}

// TestRealClientConnectOrStartChild is the child half of
// connectOrStartFromProcesses; it skips unless started by it. It reports ready
// once it holds the handle, calls ConnectOrStart when the parent closes its
// stdin, and holds the sandbox briefly so its peers overlap with a live owner
// before detaching as the launcher does.
func TestRealClientConnectOrStartChild(t *testing.T) {
	home := os.Getenv(childHomeEnv)
	if home == "" {
		t.Skip("runs only as a child of TestRealClientSandboxLifecycle")
	}
	t.Setenv("MSB_HOME", home)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	handle := handleNamed(ctx, t, realMsbClient{}, os.Getenv(childNameEnv))
	_, _ = fmt.Fprintln(os.Stdout, childReadyLine)
	_, _ = io.Copy(io.Discard, os.Stdin)

	sb, booted, err := handle.ConnectOrStart(ctx)
	if err != nil {
		t.Fatalf("ConnectOrStart: %v", err)
	}
	_, _ = fmt.Fprintln(os.Stdout, childBootedLine+strconv.FormatBool(booted))
	time.Sleep(3 * time.Second)
	if err := sb.Detach(ctx); err != nil {
		t.Errorf("Detach: %v", err)
	}
}

func volumeNamed(handles []VolumeHandle, name string) bool {
	for _, handle := range handles {
		if handle.Name() == name {
			return true
		}
	}
	return false
}

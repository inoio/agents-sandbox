//go:build msb

package msb

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// The child-process environment: when childHomeEnv is set, the test binary is a
// child of TestLifecycleContract and TestConnectOrStartChild runs one
// ConnectOrStart against the parent's runtime home and sandbox.
const (
	childHomeEnv    = "AGENTS_SANDBOX_TEST_CHILD_MSB_HOME"
	childNameEnv    = "AGENTS_SANDBOX_TEST_CHILD_SANDBOX"
	childStartAtEnv = "AGENTS_SANDBOX_TEST_CHILD_START_AT"
	childBootedLine = "connect-or-start booted="
)

// TestLifecycleContract pins the runtime semantics the project VM lifecycle
// relies on (specs/sandbox-identity.md), so an SDK bump that changes them fails
// here instead of in a user's run:
//   - creating a taken name without replace fails with "already exists";
//   - of several processes calling ConnectOrStart on one stopped sandbox,
//     exactly one reports the boot and all converge on the same sandbox;
//   - a handle whose name was destroyed and recreated is rejected as replaced.
func TestLifecycleContract(t *testing.T) {
	ensureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := realMsbClient{}
	name := "agents-sandbox-msb-lifecycle-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	createOptions := []msbSdk.SandboxOption{
		msbSdk.WithImage("alpine:3.20"),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(testIdleTimeout),
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), destroyTimeout)
		defer cleanupCancel()
		if handle, err := client.GetSandbox(cleanupCtx, name); err == nil {
			_ = handle.Destroy(cleanupCtx)
		}
	})

	if _, err := client.CreateSandbox(ctx, name, createOptions...); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if _, err := client.CreateSandbox(ctx, name, createOptions...); !IsSandboxAlreadyExists(err) {
		t.Fatalf("CreateSandbox on a taken name = %v, want an already-exists error", err)
	}

	original := handleNamed(ctx, t, client, name)
	if err := original.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if booted := connectOrStartFromProcesses(t, name, 3); booted != 1 {
		t.Fatalf("processes that booted the stopped sandbox = %d, want exactly 1", booted)
	}
	converged := handleNamed(ctx, t, client, name)
	if converged.ID() != original.ID() || converged.Status() != msbSdk.SandboxStatusRunning {
		t.Fatalf("sandbox after the race = %s (%s), want %s running",
			converged.ID(), converged.Status(), original.ID())
	}

	if err := original.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := client.CreateSandbox(ctx, name, createOptions...); err != nil {
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
// count child processes released at the same instant, as concurrent launcher
// invocations would, and returns how many of them booted it.
func connectOrStartFromProcesses(t *testing.T, name string, count int) int {
	t.Helper()
	startAt := strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10)
	outputs := make([]string, count)
	var wg sync.WaitGroup
	for index := range count {
		wg.Go(func() {
			child := exec.Command(os.Args[0], "-test.run=^TestConnectOrStartChild$", "-test.v")
			child.Env = append(os.Environ(),
				childHomeEnv+"="+isolatedRuntimeHome,
				childNameEnv+"="+name,
				childStartAtEnv+"="+startAt,
			)
			output, err := child.CombinedOutput()
			if err != nil {
				t.Errorf("child %d: %v\n%s", index, err, output)
			}
			outputs[index] = string(output)
		})
	}
	wg.Wait()
	booted := 0
	for index, output := range outputs {
		switch {
		case strings.Contains(output, childBootedLine+"true"):
			booted++
		case !strings.Contains(output, childBootedLine+"false"):
			t.Errorf("child %d reported no connect-or-start outcome:\n%s", index, output)
		}
	}
	return booted
}

// TestConnectOrStartChild is the child half of connectOrStartFromProcesses; it
// skips unless started by it. It holds the sandbox briefly so its peers overlap
// with a live owner, then detaches as the launcher does.
func TestConnectOrStartChild(t *testing.T) {
	home := os.Getenv(childHomeEnv)
	if home == "" {
		t.Skip("runs only as a child of TestLifecycleContract")
	}
	t.Setenv("MSB_HOME", home)
	startAtMillis, err := strconv.ParseInt(os.Getenv(childStartAtEnv), 10, 64)
	if err != nil {
		t.Fatalf("parse %s: %v", childStartAtEnv, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	handle := handleNamed(ctx, t, realMsbClient{}, os.Getenv(childNameEnv))
	time.Sleep(time.Until(time.UnixMilli(startAtMillis)))

	sb, booted, err := handle.ConnectOrStart(ctx)
	if err != nil {
		t.Fatalf("ConnectOrStart: %v", err)
	}
	t.Log(childBootedLine + strconv.FormatBool(booted))
	time.Sleep(3 * time.Second)
	if err := sb.Detach(ctx); err != nil {
		t.Errorf("Detach: %v", err)
	}
}

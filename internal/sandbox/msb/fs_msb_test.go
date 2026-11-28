//go:build msb

package msb

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
)

// TestRealClientFileRoundTrip drives realSandbox.FS's transfer methods against a
// real sandbox: a host file copied in reads back, a nested guest destination's
// parent is created by the caller (CopyFromHost itself does not), and a file
// copied out matches the original.
func TestRealClientFileRoundTrip(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := realMsbClient{}
	name := "agents-sandbox-msb-cp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), runtime.DestroyTimeout)
		defer cleanupCancel()
		if handle, err := client.GetSandbox(cleanupCtx, name); err == nil {
			_ = handle.Destroy(cleanupCtx, LifecycleOptions{})
		}
	})

	sb, err := client.CreateSandbox(ctx, name, SandboxSpec{
		Image:       "alpine:3.20",
		Detached:    true,
		IdleTimeout: runtime.TestIdleTimeout,
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	fs := sb.FS()

	hostIn := filepath.Join(t.TempDir(), "payload.txt")
	want := []byte("round-trip payload\n")
	if err := os.WriteFile(hostIn, want, 0o644); err != nil {
		t.Fatalf("write host input: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tmp/cp-test/nested"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fs.CopyFromHost(ctx, hostIn, "/tmp/cp-test/nested/payload.txt"); err != nil {
		t.Fatalf("CopyFromHost: %v", err)
	}
	got, err := fs.ReadString(ctx, "/tmp/cp-test/nested/payload.txt")
	if err != nil || got != string(want) {
		t.Fatalf("ReadString after CopyFromHost = (%q, %v), want %q", got, err, want)
	}

	hostOut := filepath.Join(t.TempDir(), "out.txt")
	if err := fs.CopyToHost(ctx, "/tmp/cp-test/nested/payload.txt", hostOut); err != nil {
		t.Fatalf("CopyToHost: %v", err)
	}
	roundTripped, err := os.ReadFile(hostOut)
	if err != nil || string(roundTripped) != string(want) {
		t.Fatalf("round-tripped host file = (%q, %v), want %q", roundTripped, err, want)
	}
}

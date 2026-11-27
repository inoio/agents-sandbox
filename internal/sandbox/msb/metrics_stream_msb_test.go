//go:build msb

package msb

import (
	"context"
	"strconv"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
)

// TestRealMetricsStreamDeliversSamplesAndEnds proves the stream contract the
// pressure watcher relies on against the real runtime: a running sandbox
// delivers a sample at the requested interval with a non-zero memory limit, and
// the stream ends once the sandbox stops, so the watcher stops rather than
// polling a dead VM (specs/resources/resource-limit-proposal.md §1a).
func TestRealMetricsStreamDeliversSamplesAndEnds(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	name := "agents-sandbox-msb-test-metrics-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	sb, err := msbSdk.CreateSandbox(ctx, name,
		msbSdk.WithImage("alpine:3.20"),
		msbSdk.WithReplace(),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(runtime.TestIdleTimeout),
	)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), runtime.DestroyTimeout)
		defer cleanupCancel()
		if handle, handleErr := msbSdk.GetSandbox(cleanupCtx, name); handleErr == nil {
			_ = handle.Destroy(cleanupCtx)
		}
	})

	adapter := realSandbox{sandbox: sb}
	stream, err := adapter.MetricsStream(ctx, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("MetricsStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	sample, err := stream.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if sample == nil {
		t.Fatal("Recv returned no sample for a running sandbox")
	}
	if sample.MemoryLimitBytes == 0 {
		t.Errorf("sample = %+v, want a non-zero memory limit", sample)
	}

	if err := sb.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		sample, recvErr := stream.Recv(ctx)
		if recvErr != nil || sample == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream did not end after the sandbox stopped")
		}
	}
}

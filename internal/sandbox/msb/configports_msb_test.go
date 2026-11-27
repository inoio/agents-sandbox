//go:build msb

package msb

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
)

// TestRealHandleConfigPortsRoundTrip proves the read-back contract against the
// real runtime: a sandbox created with published ports persists them in its
// inspection envelope under spec.network.ports, the launcher's read-back model
// does not carry them (the gap specs/vm/live-mount-readback.md records), and
// ConfigPorts() decodes them back. The mock-based decision tests rely on this
// shape matching the real one.
func TestRealHandleConfigPortsRoundTrip(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	name := "agents-sandbox-msb-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	created := []PortBinding{
		{Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: PortProtocolTCP},
		{Bind: "127.0.0.1", HostPort: 3000, GuestPort: 3000, Protocol: PortProtocolTCP},
	}
	if _, err := msbSdk.CreateSandbox(
		ctx,
		name,
		msbSdk.WithImage("alpine:3.20"),
		msbSdk.WithReplace(),
		msbSdk.WithDetached(),
		msbSdk.WithPortBindings(portBindings(created)...),
	); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() {
		_ = msbSdk.RemoveSandbox(context.Background(), name)
	})

	raw, err := msbSdk.GetSandbox(ctx, name)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	handle, err := sandboxHandleFrom(ctx, raw)
	if err != nil {
		t.Fatalf("resolve handle: %v", err)
	}

	dropped, err := handle.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if len(dropped.PortBindings) != 0 {
		t.Errorf(
			"Config().PortBindings = %+v, want none: the launcher's read-back model does not carry the persisted ports",
			dropped.PortBindings,
		)
	}

	readBack, err := handle.ConfigPorts()
	if err != nil {
		t.Fatalf("ConfigPorts: %v", err)
	}
	if !slices.EqualFunc(readBack, created, func(got, want PortBinding) bool {
		return got.Bind == want.Bind && got.HostPort == want.HostPort &&
			got.GuestPort == want.GuestPort && got.Protocol == want.Protocol
	}) {
		t.Errorf("ConfigPorts() = %+v, want %+v (the created bindings)", readBack, created)
	}
}

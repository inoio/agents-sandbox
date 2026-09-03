package options

import (
	"net"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestParseMemoryGigabytes(t *testing.T) {
	got := ParseMemory("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryMegabytes(t *testing.T) {
	got := ParseMemory("512M")
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryPlainNumber(t *testing.T) {
	got := ParseMemory("2048")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryLowercase(t *testing.T) {
	got := ParseMemory("2g")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestResolveTmpSizeDefaultsWhenEmpty(t *testing.T) {
	got := ResolveTmpSizeMiB("")
	if got != DefaultTmpSizeMiB {
		t.Errorf("expected default %d, got %d", DefaultTmpSizeMiB, got)
	}
}

func TestServeOnlyBindings(t *testing.T) {
	bindings := ServeOnlyBindings(ServeOnlyBasePort)
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	b := bindings[0]
	if b.Bind != ServeOnlyBindAddr {
		t.Errorf("Bind = %q, want %q", b.Bind, ServeOnlyBindAddr)
	}
	if b.HostPort != 4096 || b.GuestPort != 4096 {
		t.Errorf("HostPort/GuestPort = %d/%d, want 4096/4096", b.HostPort, b.GuestPort)
	}
	if b.Protocol != msbSdk.PortProtocolTCP {
		t.Errorf("Protocol = %v, want %v", b.Protocol, msbSdk.PortProtocolTCP)
	}
}

func TestServeOnlyOptionsSet(t *testing.T) {
	opts := RunOptions{ServeOnly: true}
	if !opts.ServeOnly {
		t.Error("expected ServeOnly=true when explicitly set")
	}
}

func TestResolveTmpSizeParsesSpec(t *testing.T) {
	got := ResolveTmpSizeMiB("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestNewReapPolicyExplicitValues(t *testing.T) {
	rp := NewReapPolicy(true, 5)
	if !rp.AutoStopOnActiveSessions {
		t.Error("expected AutoStopOnActiveSessions true")
	}
	if rp.MaxSessionRetries != 5 {
		t.Errorf("expected MaxSessionRetries 5, got %d", rp.MaxSessionRetries)
	}
}

func TestRunOptionsNetworkZeroValueEmpty(t *testing.T) {
	opts := RunOptions{}
	if !opts.Network.Empty() {
		t.Error("zero-value RunOptions.Network should be Empty (unset => default public)")
	}
}

func TestNewReapPolicyDefaultMaxSessionRetries(t *testing.T) {
	rp := NewReapPolicy(false, 0)
	if rp.AutoStopOnActiveSessions {
		t.Error("expected AutoStopOnActiveSessions false")
	}
	if rp.MaxSessionRetries != 10 {
		t.Errorf("expected MaxSessionRetries 10 (default), got %d", rp.MaxSessionRetries)
	}

	rp = NewReapPolicy(false, -1)
	if rp.MaxSessionRetries != 10 {
		t.Errorf("expected MaxSessionRetries 10 (default for negative), got %d", rp.MaxSessionRetries)
	}
}

func TestFirstFreeHostPort(t *testing.T) {
	// Occupy a high base port, then FirstFreeHostPort must skip it and return a
	// free port above it. A high port avoids collisions with services already
	// bound on the default 4096.
	ln, err := net.Listen("tcp", "127.0.0.1:5999")
	if err != nil {
		t.Skipf("cannot bind 5999 for test: %v", err)
	}
	defer ln.Close()
	if got := FirstFreeHostPort(5999); got != 6000 {
		t.Errorf("FirstFreeHostPort(5999) = %d, want 6000", got)
	}
}

func TestResolveServeHostPort(t *testing.T) {
	if got := ResolveServeHostPort(nil, false); got != 0 {
		t.Errorf("not serve-only => 0, got %d", got)
	}
	cfg := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
	}}
	if got := ResolveServeHostPort(cfg, true); got != 4096 {
		t.Errorf("reuse existing binding => 4096, got %d", got)
	}
}

func TestResolveServeHostPortProbesWhenNoBinding(t *testing.T) {
	// Serve-only with no existing published port must probe for a free one
	// rather than returning 0.
	if got := ResolveServeHostPort(nil, true); got == 0 {
		t.Error("expected a probed non-zero port when serving with no existing binding")
	}
}

func TestServeOnlyBindingsParametrized(t *testing.T) {
	got := ServeOnlyBindings(4097)
	if len(got) != 1 || got[0].HostPort != 4097 || got[0].GuestPort != 4096 {
		t.Errorf("ServeOnlyBindings(4097) = %+v, want host 4097 guest 4096", got)
	}
}

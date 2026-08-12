package options

import (
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
	bindings := ServeOnlyBindings()
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

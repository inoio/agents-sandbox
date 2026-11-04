package msb

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestParseConfigPortsMapsEveryProtocol(t *testing.T) {
	got, err := ParseConfigPorts(persistedConfigWithPorts)
	if err != nil {
		t.Fatalf("ParseConfigPorts() error = %v", err)
	}
	// The Sctp entry is unknown to the launcher and skipped, not fatal.
	want := []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
		{Bind: "127.0.0.1", HostPort: 3000, GuestPort: 3000, Protocol: msbSdk.PortProtocolTCP},
		{Bind: "127.0.0.1", HostPort: 8080, GuestPort: 5173, Protocol: msbSdk.PortProtocolUDP},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ParseConfigPorts() = %+v, want %+v", got, want)
	}
}

func TestParseConfigPortsWithoutPortsKeyIsNotAnError(t *testing.T) {
	got, err := ParseConfigPorts(`{"name":"vm","network":{"enabled":true}}`)
	if err != nil {
		t.Fatalf("ParseConfigPorts(without ports) error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseConfigPorts(without ports) = %+v, want none", got)
	}
}

func TestParseConfigPortsWithoutNetworkKeyIsNotAnError(t *testing.T) {
	got, err := ParseConfigPorts(`{"name":"vm","memory_mib":512}`)
	if err != nil {
		t.Fatalf("ParseConfigPorts(without network) error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseConfigPorts(without network) = %+v, want none", got)
	}
}

func TestParseConfigPortsEmptyArrayIsNoBindings(t *testing.T) {
	got, err := ParseConfigPorts(`{"network":{"ports":[]}}`)
	if err != nil {
		t.Fatalf("ParseConfigPorts(empty array) error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseConfigPorts(empty array) = %+v, want none", got)
	}
}

func TestParseConfigPortsRejectsMalformedJSON(t *testing.T) {
	var syntaxErr *json.SyntaxError
	if _, err := ParseConfigPorts(`{"network":{"ports":[`); !errors.As(err, &syntaxErr) {
		t.Errorf("ParseConfigPorts(malformed) error = %v, want it to wrap the JSON syntax error", err)
	}
}

// TestParseConfigPortsSkipsUnknownProtocol pins that an entry with a protocol
// this launcher does not know is skipped and the scan goes on to the next.
func TestParseConfigPortsSkipsUnknownProtocol(t *testing.T) {
	got, err := ParseConfigPorts(`{"network":{"ports":[
	  {"host_bind":"127.0.0.1","host_port":9,"guest_port":9,"protocol":"Sctp"},
	  {"host_bind":"127.0.0.1","host_port":3000,"guest_port":3000,"protocol":"Tcp"}
	]}}`)
	want := []msbSdk.PortBinding{{Bind: "127.0.0.1", HostPort: 3000, GuestPort: 3000, Protocol: msbSdk.PortProtocolTCP}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("ParseConfigPorts() = %+v, %v; want %+v", got, err, want)
	}
}

// TestRealSandboxHandleConfigPorts covers the real handle's delegation to
// ParseConfigPorts: a handle without persisted config JSON reports the decode
// error rather than a silent empty binding list, mirroring ConfigMounts.
func TestRealSandboxHandleConfigPorts(t *testing.T) {
	h := &realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	if _, err := h.ConfigPorts(); err == nil {
		t.Error("ConfigPorts() error = nil, want an error for a handle without config JSON")
	}
}

func TestMockSandboxHandleConfigPorts(t *testing.T) {
	want := []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
	}
	m := &MockSandboxHandle{PortBindings: want}
	got, err := m.ConfigPorts()
	if err != nil {
		t.Fatalf("ConfigPorts() error = %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("ConfigPorts() = %+v, want the seeded bindings", got)
	}

	failing := &MockSandboxHandle{PortsErr: errors.New("ports boom")}
	if _, err := failing.ConfigPorts(); err == nil {
		t.Error("ConfigPorts() error = nil, want the configured PortsErr")
	}
}

// persistedConfigWithPorts mirrors the real persisted shape: SandboxConfig
// flattens SandboxSpec, whose NetworkSpec carries published ports as
// PublishedPortSpec values (microsandbox-types domain.rs) — snake_case fields,
// the protocol spelled as the Rust variant name, host_bind always written.
const persistedConfigWithPorts = `{
  "name": "agents-sandbox-vm-proj-opencode",
  "memory_mib": 16384,
  "network": {
    "enabled": true,
    "ports": [
      {"host_port":4096,"guest_port":4096,"protocol":"Tcp","host_bind":"127.0.0.1"},
      {"host_port":3000,"guest_port":3000,"host_bind":"127.0.0.1"},
      {"host_port":8080,"guest_port":5173,"protocol":"Udp","host_bind":"127.0.0.1"},
      {"host_port":9000,"guest_port":9000,"protocol":"Sctp","host_bind":"127.0.0.1"}
    ]
  }
}`

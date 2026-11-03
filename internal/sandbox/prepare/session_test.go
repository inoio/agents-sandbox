package prepare

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestSessionServeHostPort(t *testing.T) {
	s := &Session{serveHostPort: 4096}
	if got := s.ServeHostPort(); got != 4096 {
		t.Errorf("ServeHostPort() = %d, want 4096", got)
	}
}

func TestSessionAccessors(t *testing.T) {
	sb := &msb.MockSandbox{}
	s := &Session{sb: sb, name: "vm-name", target: "/tmp/target", cwd: "/workspace", homeVolume: "home-vol"}
	if got := s.HomeVolume(); got != "home-vol" {
		t.Errorf("HomeVolume() = %q, want home-vol", got)
	}
	if got := s.Sandbox(); got != sb {
		t.Errorf("Sandbox() = %v, want the stored sandbox", got)
	}
	if got := s.Target(); got != "/tmp/target" {
		t.Errorf("Target() = %q, want /tmp/target", got)
	}

	nilSession := &Session{}
	if got := nilSession.Sandbox(); got != nil {
		t.Errorf("Sandbox() on nil VM = %v, want nil", got)
	}
	nilSession.Cleanup()
}

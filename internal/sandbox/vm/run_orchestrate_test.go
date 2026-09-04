package vm

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

func TestResolveServeHostPortNotServeOnly(t *testing.T) {
	if got := resolveServeHostPort(options.RunOptions{}, 0); got != 0 {
		t.Errorf("resolveServeHostPort(not serve-only) = %d, want 0", got)
	}
}

func TestResolveServeHostPortPassthrough(t *testing.T) {
	if got := resolveServeHostPort(options.RunOptions{ServeOnly: true}, 4096); got != 4096 {
		t.Errorf("resolveServeHostPort(planned 4096) = %d, want 4096", got)
	}
}

func TestResolveServeHostPortProbesWhenZero(t *testing.T) {
	got := resolveServeHostPort(options.RunOptions{ServeOnly: true}, 0)
	if got == 0 || got < options.ServeOnlyBasePort {
		t.Errorf("resolveServeHostPort(planned 0) = %d, want a probed port >= %d", got, options.ServeOnlyBasePort)
	}
}

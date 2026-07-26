package sandbox

import (
	"bytes"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
)

func TestCheckDockerLogsUnderlyingError(t *testing.T) {
	var buf bytes.Buffer
	l := output.NewPrinter(&buf, false)

	t.Setenv("PATH", "/nonexistent")
	if CheckDocker(l) {
		t.Fatal("expected CheckDocker to return false when docker is not on PATH")
	}

	out := buf.String()
	if !strings.Contains(out, "docker not found") {
		t.Errorf("expected log to contain 'docker not found', got %q", out)
	}
	if !strings.Contains(out, "executable file not found") {
		t.Errorf("expected log to contain the underlying LookPath error, got %q", out)
	}
}

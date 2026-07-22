package log

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestSpinnerNonTerminalStop(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(New(&buf, false))
	s.Start("Building image")
	s.Stop()

	output := buf.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "done\n") {
		t.Errorf("expected done, got %q", output)
	}
}

func TestSpinnerNonTerminalError(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(New(&buf, false))
	s.Start("Building image")
	s.StopError(fmt.Errorf("build failed"))

	output := buf.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "failed: build failed\n") {
		t.Errorf("expected error message, got %q", output)
	}
}

func TestSpinnerStopTwiceNoPanic(_ *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(New(&buf, false))
	s.Start("Building image")
	s.Stop()
	s.Stop()
	s.StopError(fmt.Errorf("err"))
}

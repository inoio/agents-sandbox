package opencodemsb

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestSpinnerNonTerminalStop(t *testing.T) {
	var buf bytes.Buffer
	s := &spinner{w: &buf, color: false, msg: "Building image"}
	s.start()
	s.stop()

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
	s := &spinner{w: &buf, color: false, msg: "Building image"}
	s.start()
	s.stopError(fmt.Errorf("build failed"))

	output := buf.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "failed: build failed\n") {
		t.Errorf("expected error message, got %q", output)
	}
}

func TestSpinnerStopTwiceNoPanic(t *testing.T) {
	var buf bytes.Buffer
	s := &spinner{w: &buf, color: false, msg: "Building image"}
	s.start()
	s.stop()
	s.stop()
	s.stopError(fmt.Errorf("err"))
}

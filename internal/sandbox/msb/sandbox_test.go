package msb

import (
	"errors"
	"testing"
)

// TestAttachEndedAbnormally pins the abnormal-end classification: a clean exit
// and an ordinary non-zero exit are normal, while a signal death (-1) or an
// attach error is abnormal (spec: -1 is the guest's signal death; a non-zero
// exit code is a deliberate agent exit, not a crash).
func TestAttachEndedAbnormally(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attachErr error
		exitCode  int
		want      bool
	}{
		{"clean exit", nil, 0, false},
		{"non-zero exit code", nil, 3, false},
		{"signal death", nil, -1, true},
		{"attach error", errors.New("boom"), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AttachEndedAbnormally(tc.attachErr, tc.exitCode); got != tc.want {
				t.Errorf("AttachEndedAbnormally(%v, %d) = %v, want %v", tc.attachErr, tc.exitCode, got, tc.want)
			}
		})
	}
}

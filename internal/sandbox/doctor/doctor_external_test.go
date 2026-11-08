package doctor_test

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestCheckAllUsesMockedCheckAll pins that an external test package can replace
// the CheckAll seam through doctortest, exercising only doctor's exported API.
func TestCheckAllUsesMockedCheckAll(t *testing.T) {
	tests := []struct {
		name    string
		success bool
	}{
		{"success", true},
		{"failure", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testUI := termiotest.NewTestMock(t)
			doctortest.MockedCheckAll(t, tc.success)

			if got := doctor.CheckAll(context.Background(), docker.Get(), &testUI); got != tc.success {
				t.Errorf("CheckAll() = %v, want %v", got, tc.success)
			}
		})
	}
}

// TestCheckDockerUsesMockedCheckDocker pins the same external seam replacement
// for CheckDocker.
func TestCheckDockerUsesMockedCheckDocker(t *testing.T) {
	doctortest.MockedCheckDocker(t, false)
	if err := doctor.CheckDocker(context.Background(), docker.Get()); err == nil {
		t.Fatal("CheckDocker() = nil, want the mocked failure")
	}

	doctortest.MockedCheckDocker(t, true)
	if err := doctor.CheckDocker(context.Background(), docker.Get()); err != nil {
		t.Errorf("CheckDocker() = %v, want nil from the mocked success", err)
	}
}

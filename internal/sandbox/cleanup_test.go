package sandbox

import (
	"context"
	"testing"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testhelpers"
)

func TestAutoPruneDoesNotPanic(t *testing.T) {
	testUI := testhelpers.NewTestio(t)
	AutoPrune(context.Background(), nil, time.Hour, &testUI)
}

func TestAutoPruneIsIdempotent(t *testing.T) {
	// AutoPrune uses sync.Once, so calling it twice should be safe.
	// We can't easily test that it only runs once via Prune (which calls real SDK),
	// but we can verify no panic on repeated calls.
	testUI := testhelpers.NewTestio(t)
	AutoPrune(context.Background(), nil, time.Hour, &testUI)
	AutoPrune(context.Background(), nil, time.Hour, &testUI)
}

func TestAutoPruneDefaultsToSevenDays(t *testing.T) {
	// When threshold is 0, AutoPrune should default to 7 days.
	// We can't directly assert the threshold used, but we verify zero doesn't panic.
	testUI := testhelpers.NewTestio(t)
	AutoPrune(context.Background(), nil, 0, &testUI)
}

func TestStaleReportHasAnything(t *testing.T) {
	tests := []struct {
		name   string
		report *StaleReport
		want   bool
	}{
		{
			name:   "empty report",
			report: &StaleReport{},
			want:   false,
		},
		{
			name:   "nil report",
			report: nil,
			want:   false,
		},
		{
			name:   "has pruned VMs",
			report: &StaleReport{PrunedVMs: 1},
			want:   true,
		},
		{
			name:   "has pruned volumes",
			report: &StaleReport{PrunedVolumes: 3},
			want:   true,
		},
		{
			name:   "has pruned docker images",
			report: &StaleReport{PrunedDockerImages: 2},
			want:   true,
		},
		{
			name:   "has pruned msb images",
			report: &StaleReport{PrunedMSBImages: 1},
			want:   true,
		},
		{
			name:   "has pruned task sandboxes",
			report: &StaleReport{PrunedTaskSandboxes: 5},
			want:   true,
		},
		{
			name:   "has pruned clone volumes",
			report: &StaleReport{PrunedCloneVolumes: 2},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.report.HasAnything()
			if got != tt.want {
				t.Errorf("HasAnything() = %v, want %v", got, tt.want)
			}
		})
	}
}

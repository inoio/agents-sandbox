package pruning

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPruneAgeSeconds pins the age-to-seconds conversion, including the
// boundary that a positive subsecond age must not collapse to zero (which would
// disable the runtime's age filter).
func TestPruneAgeSeconds(t *testing.T) {
	cases := []struct {
		name string
		age  time.Duration
		want uint64
	}{
		{"zero disables the filter", 0, 0},
		{"negative disables the filter", -time.Hour, 0},
		{"one nanosecond rounds up", time.Nanosecond, 1},
		{"subsecond rounds up", 500 * time.Millisecond, 1},
		{"exactly one second", time.Second, 1},
		{"one second and a nanosecond rounds up", time.Second + time.Nanosecond, 2},
		{"whole seconds are exact", 2 * time.Second, 2},
		{"a large age stays exact", 90 * time.Second, 90},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pruneAgeSeconds(tc.age); got != tc.want {
				t.Errorf("pruneAgeSeconds(%v) = %d, want %d", tc.age, got, tc.want)
			}
		})
	}
}

func TestPruneStoragePassesAgeAndDryRun(t *testing.T) {
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	if _, err := PruneStorage(context.Background(), PruneState{Age: 2 * 24 * time.Hour}, true, ui); err != nil {
		t.Fatalf("PruneStorage: %v", err)
	}
	if len(client.StoragePruneCalls) != 1 {
		t.Fatalf("StoragePruneCalls = %v, want 1", client.StoragePruneCalls)
	}
	got := client.StoragePruneCalls[0]
	if !got.DryRun {
		t.Error("DryRun = false, want true")
	}
	if want := uint64((2 * 24 * time.Hour).Seconds()); got.OlderThanSeconds != want {
		t.Errorf("OlderThanSeconds = %d, want %d", got.OlderThanSeconds, want)
	}
}

func TestPruneStorageZeroAgeDisablesAgeFilter(t *testing.T) {
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	if _, err := PruneStorage(context.Background(), PruneState{Age: 0}, false, &termio.Mock{}); err != nil {
		t.Fatalf("PruneStorage: %v", err)
	}
	if got := client.StoragePruneCalls[0].OlderThanSeconds; got != 0 {
		t.Errorf("OlderThanSeconds = %d, want 0 for an unset age", got)
	}
}

func TestPruneStorageReportsBytes(t *testing.T) {
	physical := uint64(4096)
	client := &msb.MockMsbClient{
		StoragePruneFn: func(context.Context, msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error) {
			return &msbSdk.StoragePruneReport{
				FilesRemoved:           2,
				LogicalBytesRemoved:    8192,
				PhysicalBytesReclaimed: &physical,
			}, nil
		},
	}
	msb.WithMsbMock(t, client)

	report, err := PruneStorage(context.Background(), PruneState{Age: time.Hour}, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneStorage: %v", err)
	}
	if report.FilesRemoved != 2 || report.LogicalBytesRemoved != 8192 {
		t.Errorf("report = %+v, want files 2 logical 8192", report)
	}
	if report.PhysicalBytesReclaimed == nil || *report.PhysicalBytesReclaimed != physical {
		t.Errorf("PhysicalBytesReclaimed = %v, want %d", report.PhysicalBytesReclaimed, physical)
	}
}

// TestPruneStorageUnknownPhysicalBytesStayNil pins the nil-vs-zero convention:
// a runtime that cannot measure physical reclamation must not be reported as
// having reclaimed zero bytes.
func TestPruneStorageUnknownPhysicalBytesStayNil(t *testing.T) {
	client := &msb.MockMsbClient{
		StoragePruneFn: func(context.Context, msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error) {
			return &msbSdk.StoragePruneReport{FilesRemoved: 1, LogicalBytesRemoved: 100}, nil
		},
	}
	msb.WithMsbMock(t, client)

	report, err := PruneStorage(context.Background(), PruneState{Age: time.Hour}, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneStorage: %v", err)
	}
	if report.PhysicalBytesReclaimed != nil {
		t.Errorf("PhysicalBytesReclaimed = %v, want nil", *report.PhysicalBytesReclaimed)
	}
}

func TestPruneStorageFailureWarnsAndReturnsError(t *testing.T) {
	client := &msb.MockMsbClient{
		StoragePruneFn: func(context.Context, msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error) {
			return nil, errors.New("storage boom")
		},
	}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	_, err := PruneStorage(context.Background(), PruneState{Age: time.Hour}, false, ui)
	if err == nil || !strings.Contains(err.Error(), "storage boom") {
		t.Fatalf("PruneStorage error = %v, want the SDK error", err)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool { return strings.Contains(s, "storage boom") }) {
		t.Errorf("expected a warning mentioning the failure, got %v", ui.WarnCalls)
	}
}

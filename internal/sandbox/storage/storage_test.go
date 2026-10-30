package storage

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func uint64Ptr(v uint64) *uint64 { return new(v) }

// stubPrivateHome points the package's home seam at dir for the test's duration.
func stubPrivateHome(t *testing.T, dir string) {
	t.Helper()
	original := privateHome
	privateHome = func() string { return dir }
	t.Cleanup(func() { privateHome = original })
}

func reportWithAllCategories() *msbSdk.StorageUsageReport {
	return &msbSdk.StorageUsageReport{
		Images: msbSdk.StorageCategoryUsage{
			Count:          uint64Ptr(2),
			InUse:          uint64Ptr(1),
			LogicalBytes:   uint64Ptr(100),
			AllocatedBytes: uint64Ptr(80),
		},
		Snapshots:      msbSdk.StorageCategoryUsage{Count: uint64Ptr(1), LogicalBytes: uint64Ptr(10)},
		Sandboxes:      msbSdk.StorageCategoryUsage{Count: uint64Ptr(3), LogicalBytes: uint64Ptr(30)},
		Volumes:        msbSdk.StorageCategoryUsage{Count: uint64Ptr(4), LogicalBytes: uint64Ptr(40)},
		BranchMemory:   msbSdk.StorageCategoryUsage{Count: uint64Ptr(5), LogicalBytes: uint64Ptr(50)},
		SnapshotMemory: msbSdk.StorageCategoryUsage{Count: uint64Ptr(6), LogicalBytes: uint64Ptr(60)},
	}
}

func TestObserveMapsMSBCategoriesInOrder(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return reportWithAllCategories(), nil
	}})
	stubPrivateHome(t, t.TempDir())

	report, err := Observe(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if got, want := categoryNames(report), []string{
		CategoryImages, CategorySnapshots, CategoryBranchMemory, CategorySnapshotMemory,
		CategorySandboxes, CategoryVolumes, CategoryRuntime,
	}; !slices.Equal(got, want) {
		t.Fatalf("category names = %v, want %v", got, want)
	}

	images := report.Categories[0]
	if got := images.Totals.Count; got == nil || *got != 2 {
		t.Errorf("images count = %v, want 2", got)
	}
	if got := images.Totals.InUse; got == nil || *got != 1 {
		t.Errorf("images in use = %v, want 1", got)
	}
	if got := images.Totals.LogicalBytes; got == nil || *got != 100 {
		t.Errorf("images logical bytes = %v, want 100", got)
	}
	if got := images.Totals.AllocatedBytes; got == nil || *got != 80 {
		t.Errorf("images allocated bytes = %v, want 80", got)
	}
	if images.Totals.ReclaimableBytes != nil {
		t.Errorf("images reclaimable bytes = %v, want nil", images.Totals.ReclaimableBytes)
	}

	// Each category must draw from its own SDK field; assert every one so a
	// selector returning the wrong field is caught.
	wants := []struct {
		count   uint64
		logical uint64
	}{
		{2, 100}, // images
		{1, 10},  // snapshots
		{5, 50},  // branch_memory
		{6, 60},  // snapshot_memory
		{3, 30},  // sandboxes
		{4, 40},  // volumes
	}
	for i, want := range wants {
		category := report.Categories[i]
		if got := category.Totals.Count; got == nil || *got != want.count {
			t.Errorf("category %s count = %v, want %d", category.Name, got, want.count)
		}
		if got := category.Totals.LogicalBytes; got == nil || *got != want.logical {
			t.Errorf("category %s logical bytes = %v, want %d", category.Name, got, want.logical)
		}
	}
}

func TestObservePropagatesNotes(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return &msbSdk.StorageUsageReport{
			Images: msbSdk.StorageCategoryUsage{Notes: []string{"image note"}},
			Notes:  []string{"report note"},
		}, nil
	}})
	stubPrivateHome(t, t.TempDir())

	report, err := Observe(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if !slices.Contains(report.Notes, "report note") {
		t.Errorf("report notes = %v, want the usage report note", report.Notes)
	}
	if notes := report.Categories[0].Notes; !slices.Contains(notes, "image note") {
		t.Errorf("images notes = %v, want the category note", notes)
	}
}

func TestObserveKeepsNilCountersUnknown(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return &msbSdk.StorageUsageReport{}, nil
	}})
	stubPrivateHome(t, t.TempDir())

	report, err := Observe(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	for _, c := range report.Categories[:6] {
		if c.Totals.Count != nil || c.Totals.LogicalBytes != nil ||
			c.Totals.AllocatedBytes != nil || c.Totals.ReclaimableBytes != nil {
			t.Errorf("category %s totals = %+v, want all nil (unknown)", c.Name, c.Totals)
		}
	}
}

func TestObserveUnsupportedDegradesToUnknown(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrUnsupportedOperation, Message: "remote backend"}
	}})
	stubPrivateHome(t, t.TempDir())

	report, err := Observe(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Observe() error = %v, want nil (graceful degrade)", err)
	}
	if len(report.Categories) != 7 {
		t.Fatalf("categories = %d, want 7", len(report.Categories))
	}
	for _, c := range report.Categories[:6] {
		if c.Totals.Count != nil {
			t.Errorf("category %s count = %v, want nil (unknown)", c.Name, c.Totals.Count)
		}
	}
	if len(report.Notes) == 0 {
		t.Error("report notes = empty, want an unsupported note")
	}
	if report.Categories[6].Name != CategoryRuntime {
		t.Errorf("last category = %s, want runtime", report.Categories[6].Name)
	}
}

func TestObserveMSBErrorPropagates(t *testing.T) {
	sentinel := errors.New("disk exploded")
	msb.WithMsbMock(t, &msb.MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return nil, sentinel
	}})
	stubPrivateHome(t, t.TempDir())

	if _, err := Observe(context.Background(), Options{}); !errors.Is(err, sentinel) {
		t.Fatalf("Observe() error = %v, want the sentinel cause preserved", err)
	}
}

func TestObserveIncludeObjects(t *testing.T) {
	inUse := true
	reclaimable := false
	msb.WithMsbMock(t, &msb.MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return &msbSdk.StorageUsageReport{
			Images: msbSdk.StorageCategoryUsage{
				Items: []msbSdk.StorageItemUsage{{
					Name:           "agents-sandbox/x:latest",
					Path:           "/home/.local/share/agents-sandbox/msb/images/x",
					LogicalBytes:   uint64Ptr(500),
					AllocatedBytes: uint64Ptr(400),
					InUse:          &inUse,
					Reclaimable:    &reclaimable,
					Reasons:        []string{"tagged"},
				}},
			},
		}, nil
	}})
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "bin", "msb"), 64)
	stubPrivateHome(t, home)

	report, err := Observe(context.Background(), Options{IncludeObjects: true})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if len(report.Categories[0].Objects) != 1 {
		t.Fatalf("images objects = %d, want 1", len(report.Categories[0].Objects))
	}
	object := report.Categories[0].Objects[0]
	if object.Name != "agents-sandbox/x:latest" {
		t.Errorf("object name = %q", object.Name)
	}
	if got := object.LogicalBytes; got == nil || *got != 500 {
		t.Errorf("object logical bytes = %v, want 500", got)
	}
	if object.InUse != &inUse || object.Reclaimable != &reclaimable {
		t.Errorf("object flags = %v/%v, want in-use/reclaimable", object.InUse, object.Reclaimable)
	}
	if len(object.Reasons) != 1 || object.Reasons[0] != "tagged" {
		t.Errorf("object reasons = %v", object.Reasons)
	}
	if got := report.Categories[6].Objects; len(got) != 1 {
		t.Errorf("runtime objects = %d, want 1", len(got))
	}
}

func TestObserveOmitsObjectsByDefault(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return &msbSdk.StorageUsageReport{Images: msbSdk.StorageCategoryUsage{
			Items: []msbSdk.StorageItemUsage{{Name: "x"}},
		}}, nil
	}})
	stubPrivateHome(t, t.TempDir())

	report, err := Observe(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	for _, c := range report.Categories {
		if c.Objects != nil {
			t.Errorf("category %s objects = %v, want nil", c.Name, c.Objects)
		}
	}
}

func TestObjectsFromNoItemsIsNil(t *testing.T) {
	if got := objectsFrom(nil); got != nil {
		t.Errorf("objectsFrom(nil) = %v, want nil", got)
	}
	if got := objectsFrom([]msbSdk.StorageItemUsage{}); got != nil {
		t.Errorf("objectsFrom(empty) = %v, want nil", got)
	}
}

func categoryNames(r *Report) []string {
	names := make([]string, len(r.Categories))
	for i, c := range r.Categories {
		names[i] = c.Name
	}
	return names
}

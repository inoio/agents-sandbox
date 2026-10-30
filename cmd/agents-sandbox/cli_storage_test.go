package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func storageUint(v uint64) *uint64 { return new(v) }

// writeRuntimeFile creates a file of size bytes under the private runtime home
// (the mock config path's msb directory) and returns its path.
func writeRuntimeFile(t *testing.T, subdir, name string, size int) string {
	t.Helper()
	path := filepath.Join(configpaths.Get().UserDataDir(), "msb", subdir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func storageReportWithTotals() *msbSdk.StorageUsageReport {
	return &msbSdk.StorageUsageReport{
		Images: msbSdk.StorageCategoryUsage{
			Count:          storageUint(2),
			LogicalBytes:   storageUint(100),
			AllocatedBytes: storageUint(80),
		},
		Snapshots: msbSdk.StorageCategoryUsage{Count: storageUint(1), LogicalBytes: storageUint(10)},
	}
}

func TestDF(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF)
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return storageReportWithTotals(), nil
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df: %v", err)
	}
	if !containsNormalized(ui.OutCalls, "TYPE COUNT IN USE LOGICAL SIZE RECLAIMABLE") {
		t.Errorf("OutCalls missing header; got: %v", ui.OutCalls)
	}
	for _, want := range []string{
		"images 2 - 100 B -",
		"snapshots 1 - 10 B -",
		"branch memory - - - -",
		"snapshot memory - - - -",
		"runtime 0 - 0 B -",
		"logical sizes; '-' means unknown. use --verbose for details.",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
}

func TestDFUnknownRendersDash(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF)
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return &msbSdk.StorageUsageReport{}, nil
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df: %v", err)
	}
	if !containsNormalized(ui.OutCalls, "images - - - -") {
		t.Errorf("unknown counters should render as '-'; got: %v", ui.OutCalls)
	}
}

func TestDFUnsupported(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF)
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrUnsupportedOperation, Message: "remote backend"}
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df should degrade gracefully, got: %v", err)
	}
	for _, want := range []string{
		"images - - - -",
		"storage observation unsupported by this backend",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
}

func TestDFVerboseUnsupported(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF, "--verbose")
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrUnsupportedOperation, Message: "remote backend"}
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df --verbose should degrade gracefully, got: %v", err)
	}
	for _, want := range []string{
		"TYPE COUNT IN USE LOGICAL SIZE RECLAIMABLE",
		"storage observation unsupported by this backend",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
}

func TestStorageCategoryJSONFallback(t *testing.T) {
	got := storageCategoryJSON(map[string]jsonStorageCategory{}, "missing")
	if got.Items == nil || got.Notes == nil {
		t.Errorf("fallback category should have non-nil items/notes, got %+v", got)
	}
}

func TestDFObserveError(t *testing.T) {
	runListCmdTest(t, []string{cmdDF}, func(m *sandboxmsb.MockMsbClient) {
		m.StorageUsageFn = func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return nil, errBoom
		}
	}, nil, nil, true, "observe storage")
}

func TestDFQuiet(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF, "--quiet")
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return storageReportWithTotals(), nil
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df --quiet: %v", err)
	}
	for _, want := range []string{"images", "snapshots", "branch memory", "snapshot memory", "sandboxes", "volumes", "runtime"} {
		if !containsNormalized(ui.PrintCalls, want) {
			t.Errorf("PrintCalls missing %q; got: %v", want, ui.PrintCalls)
		}
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("quiet df should not write a table, got: %v", ui.OutCalls)
	}
}

func TestDFFormatJSON(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF, "--format", "json")
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return storageReportWithTotals(), nil
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df --format json: %v", err)
	}
	got := unmarshalStorageJSON(t, ui.OutCalls)
	if got.Images.Count == nil || *got.Images.Count != 2 {
		t.Errorf("images count = %v, want 2", got.Images.Count)
	}
	if got.Images.InUse != nil {
		t.Errorf("images in_use = %v, want null", got.Images.InUse)
	}
	if got.Images.LogicalBytes == nil || *got.Images.LogicalBytes != 100 {
		t.Errorf("images logical_bytes = %v, want 100", got.Images.LogicalBytes)
	}
	if got.Images.ReclaimableLogicalBytes != nil {
		t.Errorf("images reclaimable_logical_bytes = %v, want null", got.Images.ReclaimableLogicalBytes)
	}
	if got.Images.Items == nil {
		t.Error("items = nil, want [] (always present)")
	}
	if got.Runtime.Items == nil {
		t.Error("runtime items = nil, want []")
	}
	if got.Notes == nil {
		t.Error("top-level notes = nil, want []")
	}
}

func TestDFVerbose(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF, "--verbose")
	path := writeRuntimeFile(t, "bin", "msb", 64)
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return storageReportWithTotals(), nil
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df --verbose: %v", err)
	}
	for _, want := range []string{
		"Reclaimable shows logical bytes; '-' means unknown.",
		"Physical space freed may differ because files can share disk blocks.",
		"Images (2)",
		"Runtime (1)",
		"msb",
		fmt.Sprintf("Path: %s", path),
		"Logical size: 64 B",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
}

// storageReportWithItem carries an item with boolean flags and reasons, plus a
// category note, so the verbose detail lines are exercised.
func storageReportWithItem() *msbSdk.StorageUsageReport {
	inUse := true
	reclaimable := false
	return &msbSdk.StorageUsageReport{
		Images: msbSdk.StorageCategoryUsage{
			Count:        storageUint(2),
			InUse:        storageUint(1),
			LogicalBytes: storageUint(100),
			Items: []msbSdk.StorageItemUsage{{
				Name:           "agents-sandbox/x:latest",
				Path:           "/msb/images/x",
				LogicalBytes:   storageUint(500),
				AllocatedBytes: storageUint(400),
				InUse:          &inUse,
				Reclaimable:    &reclaimable,
				Reasons:        []string{"tagged"},
			}},
			Notes: []string{"pinned"},
		},
	}
}

func TestDFVerboseDetails(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF, "--verbose")
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return storageReportWithItem(), nil
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df --verbose: %v", err)
	}
	for _, want := range []string{
		"TYPE COUNT IN USE LOGICAL SIZE RECLAIMABLE",
		"images 2 1 100 B -",
		"Images (2)",
		"agents-sandbox/x:latest",
		"Path: /msb/images/x",
		"Logical size: 500 B",
		"Allocated: 400 B",
		"In use: Yes",
		"Reclaimable: No",
		"Reason: tagged",
		"images: pinned",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
}

func TestDFVerboseFormatJSON(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdDF, "--verbose", "--format", "json")
	path := writeRuntimeFile(t, "bin", "msb", 64)
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return storageReportWithTotals(), nil
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("df --verbose --format json: %v", err)
	}
	got := unmarshalStorageJSON(t, ui.OutCalls)
	if len(got.Runtime.Items) != 1 {
		t.Fatalf("runtime items = %d, want 1", len(got.Runtime.Items))
	}
	item := got.Runtime.Items[0]
	if item.Name != "msb" || item.Path != path {
		t.Errorf("runtime item = %+v, want name=msb path=%s", item, path)
	}
	if item.LogicalBytes == nil || *item.LogicalBytes != 64 {
		t.Errorf("runtime item logical_bytes = %v, want 64", item.LogicalBytes)
	}
}

func TestDFFormatConflict(t *testing.T) {
	runListCmdTest(t, []string{cmdDF, "--quiet", "--format", "json"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "mutually exclusive")
}

func TestDFFormatUnknown(t *testing.T) {
	runListCmdTest(t, []string{cmdDF, "--format", "yaml"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "unsupported format")
}

func TestDFNoArgs(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdDF, "extra")
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an argument error for df with a positional arg")
	}
}

func TestStorageDF(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdStorage, cmdDF)
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{
		StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
			return storageReportWithTotals(), nil
		},
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("storage df: %v", err)
	}
	if !containsNormalized(ui.OutCalls, "images 2 - 100 B -") {
		t.Errorf("storage df output = %v, want the df totals table", ui.OutCalls)
	}
}

func TestStoragePruneDryRun(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdStorage, cmdPrune, "--dry-run")
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("storage prune --dry-run: %v", err)
	}
	if !slices.Contains(ui.OutCalls, staleSummary("dry-run: Would prune", 0, 0, 0)) {
		t.Errorf("storage prune output = %v, want the prune summary", ui.OutCalls)
	}
}

func TestStoragePruneNonInteractiveRequiresYes(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdStorage, cmdPrune)
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{})
	if err := cmd.Execute(); !errors.Is(err, termio.ErrConfirmationRequired) {
		t.Fatalf("storage prune error = %v, want ErrConfirmationRequired", err)
	}
}

type storageJSONUsage struct {
	Images         storageJSONCategory `json:"images"`
	Snapshots      storageJSONCategory `json:"snapshots"`
	BranchMemory   storageJSONCategory `json:"branch_memory"`
	SnapshotMemory storageJSONCategory `json:"snapshot_memory"`
	Sandboxes      storageJSONCategory `json:"sandboxes"`
	Volumes        storageJSONCategory `json:"volumes"`
	Runtime        storageJSONCategory `json:"runtime"`
	Notes          []string            `json:"notes"`
}

type storageJSONCategory struct {
	Count                   *uint64 `json:"count"`
	InUse                   *uint64 `json:"in_use"`
	LogicalBytes            *uint64 `json:"logical_bytes"`
	AllocatedBytes          *uint64 `json:"allocated_bytes"`
	ReclaimableLogicalBytes *uint64 `json:"reclaimable_logical_bytes"`
	Items                   []struct {
		Name           string  `json:"name"`
		Path           string  `json:"path"`
		LogicalBytes   *uint64 `json:"logical_bytes"`
		AllocatedBytes *uint64 `json:"allocated_bytes"`
		InUse          *bool   `json:"in_use"`
	} `json:"items"`
	Notes []string `json:"notes"`
}

func unmarshalStorageJSON(t *testing.T, outCalls []string) storageJSONUsage {
	t.Helper()
	joined := strings.Join(outCalls, "\n")
	var got storageJSONUsage
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	return got
}

package pruning

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

func TestIsStoppedStatus(t *testing.T) {
	tests := []struct {
		name   string
		status msb.SandboxStatus
		want   bool
	}{
		{"stopped", msb.SandboxStatusStopped, true},
		{"crashed", msb.SandboxStatusCrashed, true},
		{"running", msb.SandboxStatusRunning, false},
		{"draining", msb.SandboxStatusDraining, false},
		{"paused", msb.SandboxStatusPaused, false},
		{"created", msb.SandboxStatusCreated, true},
		{"starting", msb.SandboxStatusStarting, false},
		// Unknown: inactive checks fail closed, so it is not stopped.
		{"empty string", msb.SandboxStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := msb.IsSandboxInactive(tt.status)
			if got != tt.want {
				t.Errorf("IsStoppedStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestParseImageTag(t *testing.T) {
	tests := []struct {
		input      string
		wantSlug   string
		wantDigest string
	}{
		{"agents-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{"agents-sandbox/runner-myproject:latest", "myproject", ""},
		{"agents-sandbox/runner-myproject:", "myproject", ""},
		{"agents-sandbox/runner-myproject", "myproject", ""},
		{"agents-sandbox/runner-my-project-name:xYz1234AbCdEfGh", "my-project-name", "xYz1234AbCdEfGh"},
		{"agents-sandbox/runner-myproject:sha256:abc123", "myproject:sha256", "abc123"},
		{"other-image/myproject:tag", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			info := naming.ParseImageTag(tt.input)
			if info.Slug != tt.wantSlug {
				t.Errorf("naming.ParseImageTag(%q) slug = %q, want %q", tt.input, info.Slug, tt.wantSlug)
			}
			if info.Digest != tt.wantDigest {
				t.Errorf("naming.ParseImageTag(%q) digest = %q, want %q", tt.input, info.Digest, tt.wantDigest)
			}
		})
	}
}

func TestParseVMName(t *testing.T) {
	tests := []struct {
		input      string
		wantSlug   string
		wantDigest string
		wantAgent  string
	}{
		{"agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0", "agents-sandbox-1mjusbm3wikhb0", "", ""},
		{"agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0-main", "agents-sandbox-1mjusbm3wikhb0", "", "main"},
		{"agents-sandbox-vm-my-project-1mjusbm3wikhb0-develop", "my-project-1mjusbm3wikhb0", "", "develop"},
		{"agents-sandbox-vm-projectname-main", "projectname-main", "", ""},
		{"agents-sandbox-vm-myproject-abc1234567890", "myproject-abc1234567890", "", ""},
		{"agents-sandbox-vm-noHash", "noHash", "", ""},
		{"agents-sandbox-home-test", "", "", ""},
		{"agents-sandbox-vm-projectname-aB3cDe4fGhIjKl", "projectname-aB3cDe4fGhIjKl", "", ""},
		{"agents-sandbox-vm-projectname-aB3cDe4fGhIjKl-feature", "projectname-aB3cDe4fGhIjKl-feature", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			info := naming.ParseVMName(tt.input)
			if info.Slug != tt.wantSlug {
				t.Errorf("naming.ParseVMName(%q) slug = %q, want %q", tt.input, info.Slug, tt.wantSlug)
			}
			if info.Digest != tt.wantDigest {
				t.Errorf("naming.ParseVMName(%q) digest = %q, want %q", tt.input, info.Digest, tt.wantDigest)
			}
			if info.Agent != tt.wantAgent {
				t.Errorf("naming.ParseVMName(%q) agent = %q, want %q", tt.input, info.Agent, tt.wantAgent)
			}
		})
	}
}

func TestParseHomeVolumeName(t *testing.T) {
	tests := []struct {
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			"agents-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			"myproject-aB3cDe4fGhIjKl",
			"xYz1234AbCdEfGh",
		},
		{"agents-sandbox-home-myproject-abc1234567890", "myproject", "abc1234567890"},
		{"agents-sandbox-home-abc-def-gh", "abc-def", "gh"},
		{"agents-sandbox-vm-something", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			info := naming.ParseHomeVolumeName(tt.input)
			if info.Slug != tt.wantSlug {
				t.Errorf("naming.ParseHomeVolumeName(%q) slug = %q, want %q", tt.input, info.Slug, tt.wantSlug)
			}
			if info.Digest != tt.wantDigest {
				t.Errorf("naming.ParseHomeVolumeName(%q) digest = %q, want %q", tt.input, info.Digest, tt.wantDigest)
			}
		})
	}
}

func TestParseHomeVolumeNameNewFormat(t *testing.T) {
	tests := []struct {
		input      string
		wantSlug   string
		wantDigest string
	}{
		{"agents-sandbox-home-myproj-20260806T143022", "myproj", ""},
		{"agents-sandbox-home-abc-def-1mjusbm3wikhb0-20260806T143022", "abc-def-1mjusbm3wikhb0", ""},
		{"agents-sandbox-home-proj-20261231T235959", "proj", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			info := naming.ParseHomeVolumeName(tt.input)
			if info.Slug != tt.wantSlug {
				t.Errorf("slug = %q, want %q", info.Slug, tt.wantSlug)
			}
			if info.Digest != tt.wantDigest {
				t.Errorf("digest = %q, want %q", info.Digest, tt.wantDigest)
			}
		})
	}
}

func TestParseHomeVolumeNameLegacyFormat(t *testing.T) {
	info := naming.ParseHomeVolumeName("agents-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh")
	if info.Slug != "myproject-aB3cDe4fGhIjKl" {
		t.Errorf("slug = %q, want %q", info.Slug, "myproject-aB3cDe4fGhIjKl")
	}
	if info.Digest != "xYz1234AbCdEfGh" {
		t.Errorf("digest = %q, want %q", info.Digest, "xYz1234AbCdEfGh")
	}
}

func TestStaleTypeString(t *testing.T) {
	cases := []struct {
		staleType StaleType
		want      string
	}{
		{StaleTypeVM, "vm"},
		{StaleTypeVolume, "volume"},
		{StaleTypeDockerImage, "docker-image"},
		{StaleTypeMsbImage, "msb-image"},
	}
	for _, c := range cases {
		if c.staleType.String() != c.want {
			t.Errorf("StaleType(%d).String() = %q, want %q", c.staleType, c.staleType.String(), c.want)
		}
	}
}

func TestBuildPruneStateKeysByAgent(t *testing.T) {
	old := time.Now().Add(-15 * 24 * time.Hour)
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0-opencode",
				Status_:    msb.SandboxStatusRunning,
				UpdatedAt_: time.Now(),
			},
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0-pi",
				Status_:    msb.SandboxStatusStopped,
				UpdatedAt_: old,
			},
		},
	}
	msb.WithMsbMock(t, client)
	configpathstest.WithMockConfigPaths(t)

	got, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	ocKey := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	if _, ok := got.ToKeep[ocKey]; !ok {
		t.Error("expected opencode VM kept under (slug, opencode)")
	}
	piKey := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "pi"}
	if _, ok := got.ToPrune[piKey]; !ok {
		t.Error("expected pi VM pruned under (slug, pi)")
	}
}

func TestPruneAggregateParity(t *testing.T) {
	old := time.Now().Add(-15 * 24 * time.Hour)
	staleHandle := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: old,
	}
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			staleHandle,
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-live-1mjusbm3wikhb0",
				Status_:    msb.SandboxStatusRunning,
				UpdatedAt_: old,
				Image_:     "agents-sandbox/runner-live-1mjusbm3wikhb0:opencode-latest",
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "agents-sandbox-home-proj-1mjusbm3wikhb0-20260806T143022", CreatedAt_: old},
			&msb.MockVolumeHandle{Name_: "agents-sandbox-home-live-1mjusbm3wikhb0-20260806T143022", CreatedAt_: old},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "agents-sandbox/runner-proj-1mjusbm3wikhb0:old", CreatedAt_: old},
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-live-1mjusbm3wikhb0:opencode-latest",
				CreatedAt_: old,
			},
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-live-1mjusbm3wikhb0:old",
				CreatedAt_: time.Now().Add(-30 * 24 * time.Hour),
			},
		},
	}
	msb.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	configpathstest.WithMockConfigPaths(t)

	testUI := termiotest.NewTestMock(t)
	report, err := Prune(context.Background(), docker.Get(), PruneOptions{
		Age: 7 * 24 * time.Hour,
	}, &testUI)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !staleHandle.DidRemove() {
		t.Error("expected the stale project VM to be removed through its handle")
	}
	if got := len(client.RemovedVolumes); got != 0 {
		t.Errorf("RemovedVolumes = %d, want 0 (Prune never removes a home volume)", got)
	}
	if report.PrunedVolumes != 0 {
		t.Errorf("PrunedVolumes = %d, want 0", report.PrunedVolumes)
	}
	if got := len(client.RemovedImages); got != 2 {
		t.Errorf("RemovedImages = %d, want 2 (stale proj image + live surplus)", got)
	}
}

// TestPruneNeverRemovesVolumes pins the safe-tier boundary: Prune runs no
// volume phase, so a stale key's unreferenced home volume survives a prune even
// with a stale VM present (the batch/auto-prune path). Volume removal lives in
// PruneVolumes (`volume prune`) and the purge engine.
func TestPruneNeverRemovesVolumes(t *testing.T) {
	old := time.Now().Add(-15 * 24 * time.Hour)
	const slug = "proj-1mjusbm3wikhb0"
	volumeName := "agents-sandbox-home-" + slug + "-20260806T143022"
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-" + slug,
				Status_:    msb.SandboxStatusStopped,
				UpdatedAt_: old,
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: volumeName, CreatedAt_: old},
		},
	}
	msb.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)

	report, err := Prune(context.Background(), docker.Get(), PruneOptions{Age: 7 * 24 * time.Hour}, &testUI)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none (volume removal is gated)", client.RemovedVolumes)
	}
	if report.PrunedVolumes != 0 {
		t.Errorf("PrunedVolumes = %d, want 0", report.PrunedVolumes)
	}
}

// TestPruneRunsStoragePhase proves the aggregate prune runs the runtime-memory
// phase under the same age threshold and folds its result into the report.
func TestPruneRunsStoragePhase(t *testing.T) {
	physical := uint64(4096)
	client := &msb.MockMsbClient{
		StoragePruneFn: func(context.Context, msb.PruneRequest) (*msb.PruneReport, error) {
			return &msb.PruneReport{
				FilesRemoved:           3,
				LogicalBytesRemoved:    12288,
				PhysicalBytesReclaimed: &physical,
			}, nil
		},
	}
	msb.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	configpathstest.WithMockConfigPaths(t)

	report, err := Prune(context.Background(), docker.Get(), PruneOptions{Age: 7 * 24 * time.Hour}, &termiotest.Mock{})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(client.StoragePruneCalls) != 1 {
		t.Fatalf("StoragePruneCalls = %v, want 1", client.StoragePruneCalls)
	}
	if want := uint64((7 * 24 * time.Hour).Seconds()); client.StoragePruneCalls[0].OlderThanSeconds != want {
		t.Errorf("OlderThanSeconds = %d, want %d", client.StoragePruneCalls[0].OlderThanSeconds, want)
	}
	if report.Storage.FilesRemoved != 3 || report.Storage.LogicalBytesRemoved != 12288 {
		t.Errorf("Storage = %+v, want files 3 logical 12288", report.Storage)
	}
	if report.Storage.PhysicalBytesReclaimed == nil || *report.Storage.PhysicalBytesReclaimed != physical {
		t.Errorf("PhysicalBytesReclaimed = %v, want %d", report.Storage.PhysicalBytesReclaimed, physical)
	}
}

// TestPruneJoinsStorageError proves a storage-prune failure is warned and
// joined into the aggregate error instead of aborting the run.
func TestPruneJoinsStorageError(t *testing.T) {
	client := &msb.MockMsbClient{
		StoragePruneFn: func(context.Context, msb.PruneRequest) (*msb.PruneReport, error) {
			return nil, errors.New("storage boom")
		},
	}
	msb.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	configpathstest.WithMockConfigPaths(t)

	ui := &termiotest.Mock{}
	_, err := Prune(context.Background(), docker.Get(), PruneOptions{Age: 7 * 24 * time.Hour}, ui)
	if err == nil || !strings.Contains(err.Error(), "storage boom") {
		t.Fatalf("Prune error = %v, want the storage error joined", err)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool { return strings.Contains(s, "storage boom") }) {
		t.Errorf("expected a warning mentioning the failure, got %v", ui.WarnCalls)
	}
}

func TestPrune_BuildStateError(t *testing.T) {
	client := &msb.MockMsbClient{
		ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errBoom
		},
	}
	msb.WithMsbMock(t, client)
	testUI := termiotest.NewTestMock(t)
	_, err := Prune(context.Background(), docker.Get(), PruneOptions{Age: 7 * 24 * time.Hour}, &testUI)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected buildPruneState error, got %v", err)
	}
}

func TestPruneSurfacesSubPrunerError(t *testing.T) {
	client := &msb.MockMsbClient{ListImagesErr: errBoom}
	msb.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)
	_, err := Prune(context.Background(), docker.Get(), PruneOptions{Age: 7 * 24 * time.Hour}, &testUI)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected the image sub-pruner error to surface, got %v", err)
	}
}

func TestRenderPruneSummary_NilReport(t *testing.T) {
	ui := &termiotest.Mock{}
	RenderPruneSummary(ui, nil, false)
	if len(ui.OutCalls) != 0 {
		t.Errorf("RenderPruneSummary with nil report must not emit output, got %v", ui.OutCalls)
	}
}

// TestRenderPruneSummaryReportsStorageAndImages pins the extra report lines for
// the runtime-memory phase and the native image-prune report.
func TestRenderPruneSummaryReportsStorageAndImages(t *testing.T) {
	physical := uint64(4096)
	ui := &termiotest.Mock{}
	report := &StaleReport{
		PrunedSandboxes:     1,
		SkippedImagesInUse:  4,
		ImageBytesReclaimed: &physical,
		Storage: StorageReport{
			FilesRemoved:           3,
			LogicalBytesRemoved:    12288,
			PhysicalBytesReclaimed: &physical,
		},
	}
	RenderPruneSummary(ui, report, false)
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "image data: 4 skipped (in use), 4 KiB reclaimed") {
		t.Errorf("missing image-data line: %v", ui.OutCalls)
	}
	if !strings.Contains(joined, "runtime memory: 3 file(s), 12 KiB logical, 4 KiB reclaimed") {
		t.Errorf("missing runtime-memory line: %v", ui.OutCalls)
	}
}

// TestRenderPruneSummaryUnknownBytesRenderDash pins that unknown byte counts
// render as "-", never "0".
func TestRenderPruneSummaryUnknownBytesRenderDash(t *testing.T) {
	ui := &termiotest.Mock{}
	report := &StaleReport{
		SkippedImagesInUse: 2,
		Storage:            StorageReport{FilesRemoved: 1, LogicalBytesRemoved: 100},
	}
	RenderPruneSummary(ui, report, false)
	joined := strings.Join(ui.OutCalls, "\n")
	if strings.Count(joined, "- reclaimed") != 2 {
		t.Errorf("unknown bytes must render as '- reclaimed', got: %v", ui.OutCalls)
	}
}

// TestRenderPruneSummaryLineBoundaries pins when the image-data and
// runtime-memory lines appear: for a single skipped image, for known image bytes
// with no skips, and for each storage figure on its own, not only when all hold.
func TestRenderPruneSummaryLineBoundaries(t *testing.T) {
	one := uint64(1)
	cases := []struct {
		name        string
		report      *StaleReport
		wantImage   bool
		wantStorage bool
	}{
		{"image skipped count one", &StaleReport{SkippedImagesInUse: 1}, true, false},
		{"image bytes without skips", &StaleReport{ImageBytesReclaimed: &one}, true, false},
		{"storage files only", &StaleReport{Storage: StorageReport{FilesRemoved: 1}}, false, true},
		{"storage logical bytes only", &StaleReport{Storage: StorageReport{LogicalBytesRemoved: 1}}, false, true},
		{
			"storage physical bytes only",
			&StaleReport{Storage: StorageReport{PhysicalBytesReclaimed: &one}},
			false,
			true,
		},
		{"nothing to report", &StaleReport{}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := &termiotest.Mock{}
			RenderPruneSummary(ui, tc.report, false)
			gotImage := slices.ContainsFunc(
				ui.OutCalls,
				func(s string) bool { return strings.Contains(s, "image data:") },
			)
			gotStorage := slices.ContainsFunc(
				ui.OutCalls,
				func(s string) bool { return strings.Contains(s, "runtime memory:") },
			)
			if gotImage != tc.wantImage {
				t.Errorf("image data line present = %v, want %v; out=%v", gotImage, tc.wantImage, ui.OutCalls)
			}
			if gotStorage != tc.wantStorage {
				t.Errorf("runtime memory line present = %v, want %v; out=%v", gotStorage, tc.wantStorage, ui.OutCalls)
			}
		})
	}
}

// TestRenderPruneSummaryOmitsEmptyStorageLine pins that a phase that reclaimed
// nothing and reported no bytes stays off the output.
func TestRenderPruneSummaryOmitsEmptyStorageLine(t *testing.T) {
	ui := &termiotest.Mock{}
	RenderPruneSummary(ui, &StaleReport{PrunedSandboxes: 1}, false)
	if len(ui.OutCalls) != 1 {
		t.Errorf("expected only the counts line, got: %v", ui.OutCalls)
	}
}

func TestRenderPruneSummaryVerboseDetails(t *testing.T) {
	ui := &termiotest.Mock{}
	report := &StaleReport{
		PrunedSandboxes: 1,
		Details: []StaleEntry{
			{Type: StaleTypeVM, Name: "agents-sandbox-vm-proj-1mjusbm3wikhb0", Slug: "proj-1mjusbm3wikhb0"},
		},
	}
	RenderPruneSummary(ui, report, false)
	if len(ui.VerboseCalls) != 1 {
		t.Fatalf("expected one verbose detail line, got %v", ui.VerboseCalls)
	}
	if !strings.Contains(ui.VerboseCalls[0], "agents-sandbox-vm-proj-1mjusbm3wikhb0") {
		t.Errorf("verbose detail missing entry name: %q", ui.VerboseCalls[0])
	}
}

// TestPruneRetainsCurrentRunKeyImage pins the current-run exemption end to end:
// a run passes the key it is about to use, and that key's image survives even
// though its stale project VM is pruned in the same invocation.
func TestPruneRetainsCurrentRunKeyImage(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "proj-1mjusbm3wikhb0"
	key := state.Key{Slug: slug, Agent: "opencode"}
	ref := "agents-sandbox/runner-" + slug + ":opencode-latest"
	nativePruned := false
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-" + slug + "-opencode",
				Status_:    msb.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-48 * time.Hour),
				Image_:     ref,
			},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: ref, CreatedAt_: time.Now().Add(-48 * time.Hour)},
		},
		ImagePruneFn: func(context.Context) (*msb.ImagePruneReport, error) {
			nativePruned = true
			return &msb.ImagePruneReport{}, nil
		},
	}
	msb.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	testUI := termiotest.NewTestMock(t)

	if _, err := Prune(context.Background(), docker.Get(), PruneOptions{
		Age:    time.Hour,
		Retain: []state.Key{key},
	}, &testUI); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none (the current run's key is retained)", client.RemovedImages)
	}
	if nativePruned {
		t.Error("the native image-data prune must be deferred while the run protects a key")
	}
}

func TestRenderPruneSummaryDryRunAction(t *testing.T) {
	dry := &termiotest.Mock{}
	RenderPruneSummary(dry, &StaleReport{PrunedSandboxes: 1}, true)
	if len(dry.OutCalls) != 1 || !strings.HasPrefix(dry.OutCalls[0], "dry-run: Would prune") {
		t.Errorf("OutCalls = %v, want the dry-run action", dry.OutCalls)
	}

	rendered := &termiotest.Mock{}
	RenderPruneSummary(rendered, &StaleReport{}, false)
	if len(rendered.OutCalls) != 1 || !strings.HasPrefix(rendered.OutCalls[0], "Pruned") {
		t.Errorf("OutCalls = %v, want the real action", rendered.OutCalls)
	}
}

// TestRenderPruneSummaryReportsInternalState pins that the state sweep's
// removals appear on their own line, so a prune that reclaimed only internal
// state still reports what it did.
func TestRenderPruneSummaryReportsInternalState(t *testing.T) {
	ui := &termiotest.Mock{}
	report := &StaleReport{
		State: StateSweepReport{
			ClaimsRemoved:   2,
			LeasesRemoved:   1,
			StatesRemoved:   3,
			StaleStatesKept: 4,
			DirsRemoved:     1,
		},
	}
	RenderPruneSummary(ui, report, false)
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "internal state: 2 claims, 1 leases, 3 state files removed, 4 kept, 1 dirs removed") {
		t.Errorf("missing internal-state line: %v", ui.OutCalls)
	}
}

// TestRenderPruneSummaryOmitsEmptyStateLine pins that a state sweep that
// reclaimed nothing stays off the output.
func TestRenderPruneSummaryOmitsEmptyStateLine(t *testing.T) {
	ui := &termiotest.Mock{}
	RenderPruneSummary(ui, &StaleReport{State: StateSweepReport{StaleStatesKept: 1}}, false)
	if len(ui.OutCalls) != 1 {
		t.Errorf("out = %v, want only the counts line (a no-removal state sweep is not rendered)", ui.OutCalls)
	}
}

func TestInvokePruneFunc(t *testing.T) {
	t.Run("builds state and invokes the pruner", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
					Status_:    msb.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
				},
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termiotest.Mock{}
		var gotState PruneState
		got, err := InvokePruneFunc(context.Background(),
			func(_ context.Context, state PruneState, _ bool, _ termio.UI) (int, error) {
				gotState = state
				return 42, nil
			},
			7*24*time.Hour, false, ui)
		if err != nil {
			t.Fatalf("InvokePruneFunc: %v", err)
		}
		if got != 42 {
			t.Errorf("InvokePruneFunc report = %d, want 42", got)
		}
		if _, ok := gotState.ToPrune[state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: ""}]; !ok {
			t.Errorf("pruner did not receive built PruneState, got %v", gotState)
		}
	})

	t.Run("propagates buildPruneState error", func(t *testing.T) {
		client := &msb.MockMsbClient{
			ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
				return nil, errors.New("boom")
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termiotest.Mock{}
		invoked := false
		_, err := InvokePruneFunc(context.Background(),
			func(context.Context, PruneState, bool, termio.UI) (int, error) {
				invoked = true
				return 0, nil
			},
			time.Hour, false, ui)
		if err == nil {
			t.Fatal("expected error from buildPruneState")
		}
		if invoked {
			t.Error("pruner must not be invoked when state build fails")
		}
	})

	t.Run("propagates pruner error", func(t *testing.T) {
		msb.WithMsbMock(t, &msb.MockMsbClient{})
		ui := &termiotest.Mock{}
		_, err := InvokePruneFunc(context.Background(),
			func(context.Context, PruneState, bool, termio.UI) (int, error) {
				return 0, errors.New("prune boom")
			},
			time.Hour, false, ui)
		if err == nil || err.Error() != "prune boom" {
			t.Errorf("expected pruner error to propagate, got %v", err)
		}
	})
}

// TestPruneNonDryRunSweepsState pins that a real (non-dry-run) Prune runs the
// state phase: a state recording no home volume is reclaimed, so the phase is
// not silently skipped.
func TestPruneNonDryRunSweepsState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	dockertest.WithNoopDockerMock(t)
	testUI := termiotest.NewTestMock(t)

	const slug = "sweep-non-dry-run"
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{ImageDigest: "digest"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	report, err := Prune(context.Background(), docker.Get(), PruneOptions{Age: time.Hour}, &testUI)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if report.State.StatesRemoved != 1 {
		t.Errorf("StatesRemoved = %d, want 1 (a non-dry-run must sweep state)", report.State.StatesRemoved)
	}
	if _, err := state.ReadState(agentKey); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("the volume-less state file should be removed, read err = %v", err)
	}
}

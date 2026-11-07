package view

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestNewPruneReportJSONMapsCountsAndDetails(t *testing.T) {
	report := &pruning.StaleReport{
		PrunedSandboxes: 1, PrunedVolumes: 2, PrunedSandboxImages: 3, PrunedDockerImages: 4,
		Details: []pruning.StaleEntry{{Name: "vm", Slug: "s", Digest: "d", StaleFor: 90 * time.Second}},
	}
	got := NewPruneReportJSON(report, true)
	if !got.DryRun || got.Sandboxes != 1 || got.Volumes != 2 || got.Images != 3 || got.DockerImages != 4 {
		t.Errorf("counts = %+v", got)
	}
	if len(got.Details) != 1 || got.Details[0].StaleForSeconds != 90 || got.Details[0].Name != "vm" {
		t.Errorf("details = %+v", got.Details)
	}
}

func TestNewPruneReportJSONIncludesStorageAndImageReport(t *testing.T) {
	physical := uint64(4096)
	report := &pruning.StaleReport{
		SkippedImagesInUse:  4,
		ImageBytesReclaimed: &physical,
		Storage: pruning.StorageReport{
			FilesRemoved:           3,
			LogicalBytesRemoved:    12288,
			PhysicalBytesReclaimed: &physical,
		},
	}
	got := NewPruneReportJSON(report, false)
	if got.SkippedImagesInUse != 4 || got.ImageBytesReclaimed == nil || *got.ImageBytesReclaimed != physical {
		t.Errorf("image fields = %+v", got)
	}
	if got.RuntimeFilesRemoved != 3 || got.RuntimeLogicalBytesRemoved != 12288 ||
		got.RuntimePhysicalBytesReclaimed == nil {
		t.Errorf("storage fields = %+v", got)
	}

	encoded := marshal(t, got)
	for _, key := range []string{
		`"skipped_images_in_use":4`,
		`"image_bytes_reclaimed":4096`,
		`"runtime_files_removed":3`,
		`"runtime_logical_bytes_removed":12288`,
		`"runtime_physical_bytes_reclaimed":4096`,
	} {
		if !strings.Contains(encoded, key) {
			t.Errorf("JSON missing %s: %s", key, encoded)
		}
	}
}

func TestNewPruneReportJSONUnknownBytesMarshalNull(t *testing.T) {
	got := NewPruneReportJSON(&pruning.StaleReport{Storage: pruning.StorageReport{FilesRemoved: 1}}, false)
	encoded := marshal(t, got)
	for _, key := range []string{`"image_bytes_reclaimed":null`, `"runtime_physical_bytes_reclaimed":null`} {
		if !strings.Contains(encoded, key) {
			t.Errorf("unknown bytes must marshal as null, missing %s: %s", key, encoded)
		}
	}
}

func TestNewImageReportJSONIncludesNativeReport(t *testing.T) {
	physical := uint64(2048)
	got := NewImageReportJSON(
		pruning.ImageReport{MSBImagesPruned: 1, SkippedInUse: 4, BytesReclaimed: &physical},
		false,
	)
	if got.SkippedInUse != 4 || got.BytesReclaimed == nil || *got.BytesReclaimed != physical {
		t.Errorf("native fields = %+v", got)
	}
	encoded := marshal(t, got)
	for _, key := range []string{`"skipped_in_use":4`, `"bytes_reclaimed":2048`} {
		if !strings.Contains(encoded, key) {
			t.Errorf("JSON missing %s: %s", key, encoded)
		}
	}
}

// TestImageReportJSONIncludesDockerDetails pins that the image-prune JSON
// details surface docker-image entries: the type rendered from StaleType, the
// image id as name and the stale age measured from Created.
func TestImageReportJSONIncludesDockerDetails(t *testing.T) {
	got := NewImageReportJSON(pruning.ImageReport{
		DockerImagesPruned: 1,
		Details: []pruning.StaleEntry{{
			Type:     pruning.StaleTypeDockerImage,
			Name:     "sha256:deadbeef",
			StaleFor: 48 * time.Hour,
		}},
	}, false)
	if got.DockerImages != 1 {
		t.Errorf("DockerImages = %d, want 1", got.DockerImages)
	}
	if len(got.Details) != 1 {
		t.Fatalf("Details = %v, want one entry", got.Details)
	}
	detail := got.Details[0]
	if detail.Type != "docker-image" {
		t.Errorf("Type = %q, want docker-image", detail.Type)
	}
	if detail.Name != "sha256:deadbeef" {
		t.Errorf("Name = %q, want the image id", detail.Name)
	}
	if detail.StaleForSeconds != int64((48 * time.Hour).Seconds()) {
		t.Errorf("StaleForSeconds = %d, want %d", detail.StaleForSeconds, int64((48 * time.Hour).Seconds()))
	}
}

// TestPruneReportJSONIncludesDockerDetails pins that the full prune-report JSON
// details carry docker-image entries alongside msb-image ones, each with its
// type string, identity and stale age.
func TestPruneReportJSONIncludesDockerDetails(t *testing.T) {
	got := NewPruneReportJSON(&pruning.StaleReport{
		PrunedSandboxImages: 1,
		PrunedDockerImages:  1,
		Details: []pruning.StaleEntry{
			{
				Type:     pruning.StaleTypeMsbImage,
				Name:     "agents-sandbox/runner-proj-1mjusbm3wikhb0:old",
				Slug:     "proj-1mjusbm3wikhb0",
				Digest:   "digest1",
				StaleFor: 7 * 24 * time.Hour,
			},
			{
				Type:     pruning.StaleTypeDockerImage,
				Name:     "sha256:deadbeef",
				StaleFor: 48 * time.Hour,
			},
		},
	}, false)
	if got.Images != 1 || got.DockerImages != 1 {
		t.Errorf("counts = %+v, want one msb and one docker image", got)
	}
	if len(got.Details) != 2 {
		t.Fatalf("Details = %v, want two entries", got.Details)
	}
	msbDetail, dockerDetail := got.Details[0], got.Details[1]
	if msbDetail.Type != "msb-image" || msbDetail.Name != "agents-sandbox/runner-proj-1mjusbm3wikhb0:old" ||
		msbDetail.Slug != "proj-1mjusbm3wikhb0" || msbDetail.Digest != "digest1" ||
		msbDetail.StaleForSeconds != int64((7*24*time.Hour).Seconds()) {
		t.Errorf("msb detail = %+v, want type, name, slug, digest and stale age", msbDetail)
	}
	if dockerDetail.Type != "docker-image" || dockerDetail.Name != "sha256:deadbeef" ||
		dockerDetail.Slug != "" || dockerDetail.StaleForSeconds != int64((48*time.Hour).Seconds()) {
		t.Errorf("docker detail = %+v, want type, id, no slug and stale age", dockerDetail)
	}
	encoded := marshal(t, got)
	for _, key := range []string{`"type":"msb-image"`, `"type":"docker-image"`, `"stale_for_seconds":604800`} {
		if !strings.Contains(encoded, key) {
			t.Errorf("JSON missing %s: %s", key, encoded)
		}
	}
}

func TestGroupReportJSONsKeepEmptyDetailsAsArray(t *testing.T) {
	values := []any{
		NewImageReportJSON(pruning.ImageReport{MSBImagesPruned: 1}, false),
		NewVolumeReportJSON(pruning.VolumeReport{VolumesPruned: 1}, false),
		NewSandboxReportJSON(pruning.SandboxReport{VMsPruned: 1}, false),
	}
	for _, value := range values {
		if got := marshal(t, value); !strings.Contains(got, `"details":[]`) {
			t.Errorf("empty details must marshal as [], got %s", got)
		}
	}
}

// TestNewRemovalPlanJSONMapsArtifacts pins the --format json shape of a purge
// plan: every artifact list is carried over, keys are rendered slug/agent, and
// the dry-run flag is preserved.
func TestNewRemovalPlanJSONMapsArtifacts(t *testing.T) {
	plan := &pruning.RemovalPlan{
		Slugs:          []string{"myproj-1mjusbm3wikhb0"},
		Keys:           []state.Key{{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}},
		Sandboxes:      []string{"myproj-1mjusbm3wikhb0-opencode"},
		Volumes:        []string{"myproj-1mjusbm3wikhb0-opencode-20260812T123456"},
		Images:         []string{"myproj-1mjusbm3wikhb0:opencode-latest"},
		StateFiles:     []string{"/state/myproj-1mjusbm3wikhb0/opencode/state.yaml"},
		Claims:         []string{"myproj-1mjusbm3wikhb0/token"},
		AdoptionClaims: []string{"myproj-1mjusbm3wikhb0/opencode"},
	}
	got := NewRemovalPlanJSON(plan, true)
	if !got.DryRun {
		t.Errorf("DryRun = false, want true")
	}
	if !slices.Equal(got.Slugs, plan.Slugs) || !slices.Equal(got.Sandboxes, plan.Sandboxes) ||
		!slices.Equal(got.Volumes, plan.Volumes) || !slices.Equal(got.Images, plan.Images) ||
		!slices.Equal(got.StateFiles, plan.StateFiles) || !slices.Equal(got.Claims, plan.Claims) ||
		!slices.Equal(got.AdoptionClaims, plan.AdoptionClaims) {
		t.Errorf("artifact lists = %+v", got)
	}
	if !slices.Equal(got.Keys, []string{"myproj-1mjusbm3wikhb0/opencode"}) {
		t.Errorf("Keys = %v, want the slug/agent key", got.Keys)
	}
	encoded := marshal(t, got)
	for _, key := range []string{
		`"dry_run":true`,
		`"slugs":["myproj-1mjusbm3wikhb0"]`,
		`"keys":["myproj-1mjusbm3wikhb0/opencode"]`,
		`"volumes":["myproj-1mjusbm3wikhb0-opencode-20260812T123456"]`,
		`"state_files":["/state/myproj-1mjusbm3wikhb0/opencode/state.yaml"]`,
		`"adoption_claims":["myproj-1mjusbm3wikhb0/opencode"]`,
	} {
		if !strings.Contains(encoded, key) {
			t.Errorf("JSON missing %s: %s", key, encoded)
		}
	}
}

// TestNewRemovalPlanJSONLegacyKeyRendersSlugOnly pins that a key with no agent
// (the legacy agent-less state file) renders as the bare slug in the keys list.
func TestNewRemovalPlanJSONLegacyKeyRendersSlugOnly(t *testing.T) {
	got := NewRemovalPlanJSON(&pruning.RemovalPlan{
		Keys: []state.Key{{Slug: "myproj-1mjusbm3wikhb0", Agent: ""}},
	}, false)
	if !slices.Equal(got.Keys, []string{"myproj-1mjusbm3wikhb0"}) {
		t.Errorf("Keys = %v, want the bare slug", got.Keys)
	}
}

// TestNewRemovalPlanJSONEmptyArtifactsMarshalAsArrays pins that an empty plan
// never emits null for an artifact list, so consumers can range over the keys.
func TestNewRemovalPlanJSONEmptyArtifactsMarshalAsArrays(t *testing.T) {
	got := NewRemovalPlanJSON(&pruning.RemovalPlan{}, false)
	encoded := marshal(t, got)
	for _, key := range []string{
		`"slugs":[]`, `"keys":[]`, `"sandboxes":[]`, `"volumes":[]`,
		`"images":[]`, `"state_files":[]`, `"claims":[]`, `"adoption_claims":[]`,
	} {
		if !strings.Contains(encoded, key) {
			t.Errorf("empty artifact must marshal as [], missing %s: %s", key, encoded)
		}
	}
}

// TestRemovalPlanRenderNamesVolumes pins that the human renderer names every
// artifact the purge will delete, so the confirmation covers exactly what is
// removed.
func TestRemovalPlanRenderNamesVolumes(t *testing.T) {
	ui := termio.Mock{}
	plan := &pruning.RemovalPlan{
		Slugs:          []string{"myproj-1mjusbm3wikhb0"},
		Sandboxes:      []string{"myproj-1mjusbm3wikhb0-opencode"},
		Volumes:        []string{"myproj-1mjusbm3wikhb0-opencode-20260812T123456"},
		Images:         []string{"myproj-1mjusbm3wikhb0:opencode-latest"},
		StateFiles:     []string{"/state/myproj-1mjusbm3wikhb0/opencode/state.yaml"},
		Claims:         []string{"myproj-1mjusbm3wikhb0/token"},
		AdoptionClaims: []string{"myproj-1mjusbm3wikhb0/opencode"},
	}
	RenderRemovalPlan(plan, &ui, false)
	rendered := strings.Join(ui.PrintCalls, "\n")
	for _, want := range []string{
		"Purge will remove:",
		"project: myproj-1mjusbm3wikhb0",
		"sandboxes:",
		"- myproj-1mjusbm3wikhb0-opencode",
		"home volumes:",
		"- myproj-1mjusbm3wikhb0-opencode-20260812T123456",
		"runner images:",
		"- myproj-1mjusbm3wikhb0:opencode-latest",
		"state files:",
		"- /state/myproj-1mjusbm3wikhb0/opencode/state.yaml",
		"claims:",
		"- myproj-1mjusbm3wikhb0/token",
		"adoption claims:",
		"- myproj-1mjusbm3wikhb0/opencode",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
}

// TestRemovalPlanRenderDryRunPhrasesWouldRemove pins the dry-run wording, so a
// preview never reads as a committed deletion.
func TestRemovalPlanRenderDryRunPhrasesWouldRemove(t *testing.T) {
	ui := termio.Mock{}
	RenderRemovalPlan(&pruning.RemovalPlan{
		Slugs:   []string{"myproj-1mjusbm3wikhb0"},
		Volumes: []string{"myproj-1mjusbm3wikhb0-opencode-20260812T123456"},
	}, &ui, true)
	rendered := strings.Join(ui.PrintCalls, "\n")
	if !strings.Contains(rendered, "Purge would remove:") {
		t.Errorf("dry run must phrase as 'would remove', got:\n%s", rendered)
	}
}

// TestRemovalPlanRenderListsEverySlug pins the multi-project form, which a
// cwd-implicit purge produces when the checkout has legacy repo slugs.
func TestRemovalPlanRenderListsEverySlug(t *testing.T) {
	ui := termio.Mock{}
	RenderRemovalPlan(&pruning.RemovalPlan{
		Slugs:   []string{"myproj-1mjusbm3wikhb0", "legacy-1mjusbm3wikhb0"},
		Volumes: []string{"myproj-1mjusbm3wikhb0-opencode-20260812T123456"},
	}, &ui, false)
	rendered := strings.Join(ui.PrintCalls, "\n")
	for _, want := range []string{"projects:", "- myproj-1mjusbm3wikhb0", "- legacy-1mjusbm3wikhb0"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
}

// TestRemovalPlanRenderEmptyPlanPrintsOnlyHeader pins that a plan with no
// artifacts (a degenerate plan never produced by PlanRemoval) renders only the
// header and no artifact lines.
func TestRemovalPlanRenderEmptyPlanPrintsOnlyHeader(t *testing.T) {
	ui := termio.Mock{}
	RenderRemovalPlan(&pruning.RemovalPlan{}, &ui, false)
	rendered := strings.Join(ui.PrintCalls, "\n")
	if !strings.Contains(rendered, "Purge will remove:") || strings.Contains(rendered, "- ") {
		t.Errorf("empty plan must render only the header, got:\n%s", rendered)
	}
}

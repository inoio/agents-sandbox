package pruning

import (
	"context"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestIsStoppedStatus(t *testing.T) {
	tests := []struct {
		name   string
		status msbSdk.SandboxStatus
		want   bool
	}{
		{"stopped", msbSdk.SandboxStatusStopped, true},
		{"crashed", msbSdk.SandboxStatusCrashed, true},
		{"running", msbSdk.SandboxStatusRunning, false},
		{"draining", msbSdk.SandboxStatusDraining, false},
		{"paused", msbSdk.SandboxStatusPaused, false},
		{"created", msbSdk.SandboxStatusCreated, true},
		{"starting", msbSdk.SandboxStatusStarting, false},
		// Unknown: inactive checks fail closed, so it is not stopped.
		{"empty string", msbSdk.SandboxStatus(""), false},
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
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: time.Now(),
			},
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0-pi",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: old,
			},
		},
	}
	msb.WithMsbMock(t, client)
	cp.WithMockConfigPaths(t)

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
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: old,
	}
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			staleHandle,
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-live-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusRunning,
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
	docker.WithNoopDockerMock(t)
	cp.WithMockConfigPaths(t)

	testUI := termio.NewTestMock(t)
	if _, err := Prune(context.Background(), 7*24*time.Hour, false, &testUI); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !staleHandle.DidRemove() {
		t.Error("expected the stale project VM to be removed through its handle")
	}
	if got := len(client.RemovedVolumes); got != 1 {
		t.Errorf("RemovedVolumes = %d, want 1", got)
	}
	if got := len(client.RemovedImages); got != 2 {
		t.Errorf("RemovedImages = %d, want 2 (stale proj image + live surplus)", got)
	}
}

// TestPruneRetainsCurrentRunKeyImage pins the current-run exemption end to end:
// a run passes the key it is about to use, and that key's image survives even
// though its stale project VM is pruned in the same invocation.
func TestPruneRetainsCurrentRunKeyImage(t *testing.T) {
	cp.WithMockConfigPaths(t)
	const slug = "proj-1mjusbm3wikhb0"
	key := state.Key{Slug: slug, Agent: "opencode"}
	ref := "agents-sandbox/runner-" + slug + ":opencode-latest"
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-" + slug + "-opencode",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-48 * time.Hour),
				Image_:     ref,
			},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: ref, CreatedAt_: time.Now().Add(-48 * time.Hour)},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	testUI := termio.NewTestMock(t)

	if _, err := Prune(context.Background(), time.Hour, false, &testUI, key); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none (the current run's key is retained)", client.RemovedImages)
	}
}

func TestPrune_BuildStateError(t *testing.T) {
	client := &msb.MockMsbClient{
		ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errBoom
		},
	}
	msb.WithMsbMock(t, client)
	testUI := termio.NewTestMock(t)
	_, err := Prune(context.Background(), 7*24*time.Hour, false, &testUI)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected buildPruneState error, got %v", err)
	}
}

func TestPruneSurfacesSubPrunerError(t *testing.T) {
	client := &msb.MockMsbClient{
		ListVolumesFn: func(context.Context) ([]msb.VolumeHandle, error) {
			return nil, errBoom
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	cp.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	_, err := Prune(context.Background(), 7*24*time.Hour, false, &testUI)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected the volume sub-pruner error to surface, got %v", err)
	}
}

func TestRenderPruneSummary_NilReport(t *testing.T) {
	ui := &termio.Mock{}
	RenderPruneSummary(ui, nil, false)
	if len(ui.OutCalls) != 0 {
		t.Errorf("RenderPruneSummary with nil report must not emit output, got %v", ui.OutCalls)
	}
}

func TestRenderPruneSummaryVerboseDetails(t *testing.T) {
	ui := &termio.Mock{}
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

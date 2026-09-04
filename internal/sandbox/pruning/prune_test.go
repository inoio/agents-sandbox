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

type slugDigestTest struct {
	name       string
	input      string
	wantSlug   string
	wantDigest string
	wantAgent  string
}

func runSlugDigestTests(t *testing.T, tests []slugDigestTest) {
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := naming.ArtifactFor(tt.input)
			if info.Slug != tt.wantSlug {
				t.Errorf("naming.ArtifactFor(%q) slug = %q, want %q", tt.input, info.Slug, tt.wantSlug)
			}
			if info.Digest != tt.wantDigest {
				t.Errorf("naming.ArtifactFor(%q) digest = %q, want %q", tt.input, info.Digest, tt.wantDigest)
			}
			if info.Agent != tt.wantAgent {
				t.Errorf("naming.ArtifactFor(%q) agent = %q, want %q", tt.input, info.Agent, tt.wantAgent)
			}
		})
	}
}

func TestArtifactFor_ImageReferences(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "runner with digest tag",
			input:      "agents-sandbox/runner-myproject:xYz1234AbCdEfGh",
			wantSlug:   "myproject",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "runner with latest tag",
			input:      "agents-sandbox/runner-myproject:latest",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner with empty tag",
			input:      "agents-sandbox/runner-myproject:",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner no tag at all",
			input:      "agents-sandbox/runner-myproject",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner with complex slug",
			input:      "agents-sandbox/runner-my-project-name:xYz1234AbCdEfGh",
			wantSlug:   "my-project-name",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "runner with multiple colons in tag (uses LastIndex)",
			input:      "agents-sandbox/runner-myproject:sha256:abc123",
			wantSlug:   "myproject:sha256",
			wantDigest: "abc123",
		},
		{
			name:       "base image excluded-like prefix still parsed",
			input:      "agents-sandbox/runner-base",
			wantSlug:   "base",
			wantDigest: "",
		},
		{name: "base image with tag", input: "agents-sandbox/runner-base:latest", wantSlug: "base", wantDigest: ""},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_VMNames(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "simple vm name without hash suffix",
			input:      "agents-sandbox-vm-projectname-main",
			wantSlug:   "projectname-main",
			wantDigest: "",
		},
		{
			name:       "slug with dash and branch with dash",
			input:      "agents-sandbox-vm-my-project-feature-branch",
			wantSlug:   "my-project-feature-branch",
			wantDigest: "",
		},
		{
			name:       "single word slug with single word branch",
			input:      "agents-sandbox-vm-myproject-main",
			wantSlug:   "myproject-main",
			wantDigest: "",
		},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_HomeVolumes(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "home volume with slug and digest",
			input:      "agents-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			wantSlug:   "myproject-aB3cDe4fGhIjKl",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "home volume slug with dashes",
			input:      "agents-sandbox-home-my-project-aB3cDe4fGhIjKl",
			wantSlug:   "my-project",
			wantDigest: "aB3cDe4fGhIjKl",
		},
		{
			name:       "home volume single part slug",
			input:      "agents-sandbox-home-myproject-xYz1234",
			wantSlug:   "myproject",
			wantDigest: "xYz1234",
		},
		{
			name:       "home volume only two parts (slug-digest)",
			input:      "agents-sandbox-home-proj-abc123",
			wantSlug:   "proj",
			wantDigest: "abc123",
		},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_TaskSandboxes(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "task sandbox",
			input:      "agents-sandbox-task-prefill-proj-1719432000",
			wantSlug:   "prefill-proj",
			wantDigest: "",
		},
		{
			name:       "task sandbox single slug part before dash",
			input:      "agents-sandbox-task-fill-proj",
			wantSlug:   "fill",
			wantDigest: "",
		},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_VMWithHashSuffix(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "vm with 14-char hash suffix (no branch)",
			input:      "agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0",
			wantSlug:   "agents-sandbox-1mjusbm3wikhb0",
			wantDigest: "",
			wantAgent:  "",
		},
		{
			name:       "vm with 14-char hash and branch",
			input:      "agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0-main",
			wantSlug:   "agents-sandbox-1mjusbm3wikhb0",
			wantDigest: "",
			wantAgent:  "main",
		},
		{
			name:       "vm with 14-char hash, slug with dash, and branch",
			input:      "agents-sandbox-vm-my-project-1mjusbm3wikhb0-develop",
			wantSlug:   "my-project-1mjusbm3wikhb0",
			wantDigest: "",
			wantAgent:  "develop",
		},
		{
			name:       "user's case: VM without branch matches image slug",
			input:      "agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0",
			wantSlug:   "agents-sandbox-1mjusbm3wikhb0",
			wantDigest: "",
			wantAgent:  "",
		},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_UnrecognizedPrefixes(t *testing.T) {
	tests := []slugDigestTest{
		{name: "random string", input: "some-random-name", wantSlug: "", wantDigest: ""},
		{name: "empty string", input: "", wantSlug: "", wantDigest: ""},
		{name: "similar but wrong prefix", input: "agents-sandbox-other-myslug", wantSlug: "", wantDigest: ""},
		{name: "just the prefix no remainder for vm", input: "agents-sandbox-vm-", wantSlug: "", wantDigest: ""},
		{
			name:       "just the prefix home with single part after",
			input:      "agents-sandbox-home-",
			wantSlug:   "",
			wantDigest: "",
		},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_VMOnlyTwoParts(t *testing.T) {
	// VM with only two parts after prefix (e.g. name-branch, but we have name-branch).
	// "agents-sandbox-vm-proj-main" → parts=["proj","main"] → slug = "proj".
	tests := []struct {
		name     string
		input    string
		wantSlug string
	}{
		{
			name:     "two parts vm",
			input:    "agents-sandbox-vm-proj-main",
			wantSlug: "proj-main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := naming.ArtifactFor(tt.input).Slug
			if slug != tt.wantSlug {
				t.Errorf("naming.ArtifactFor(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
		})
	}
}

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
		// Unknown: IsSandboxActive returns false, so !IsSandboxActive = true.
		{"empty string", msbSdk.SandboxStatus(""), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := !msb.IsSandboxActive(tt.status)
			if got != tt.want {
				t.Errorf("IsStoppedStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestArtifactFor_ComplexSlugNames(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "home with long slug containing hashes",
			input:      "agents-sandbox-home-abcdef-gH1234AB5678CD-eF9012gH3456iJ",
			wantSlug:   "abcdef-gH1234AB5678CD",
			wantDigest: "eF9012gH3456iJ",
		},
		{
			name:       "vm with two-part name",
			input:      "agents-sandbox-vm-acme-corp",
			wantSlug:   "acme-corp",
			wantDigest: "",
		},
	}

	runSlugDigestTests(t, tests)
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
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: old,
			},
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
	if err := Prune(context.Background(), 7*24*time.Hour, false, &testUI); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := len(client.RemovedSandboxes); got != 1 {
		t.Errorf("RemovedSandboxes = %d, want 1", got)
	}
	if got := len(client.RemovedVolumes); got != 1 {
		t.Errorf("RemovedVolumes = %d, want 1", got)
	}
	if got := len(client.RemovedImages); got != 2 {
		t.Errorf("RemovedImages = %d, want 2 (stale proj image + live surplus)", got)
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
	err := Prune(context.Background(), 7*24*time.Hour, false, &testUI)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected buildPruneState error, got %v", err)
	}
}

func TestPrintPruneSummary_NilReport(t *testing.T) {
	ui := &termio.Mock{}
	printPruneSummary(ui, nil, false)
	if len(ui.OutCalls) != 0 {
		t.Errorf("printPruneSummary with nil report must not emit output, got %v", ui.OutCalls)
	}
}

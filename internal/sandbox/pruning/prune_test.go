package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

type slugDigestTest struct {
	name       string
	input      string
	wantSlug   string
	wantDigest string
}

// homeVolumeStateYAML is a state.yaml referring to pruneTestSlug's "current"
// volume so the family of pruneActiveVMHomeVolumes tests share identical state.
const (
	homeVolumeStateYAML = "home_volume: opencode-sandbox-home-" + pruneTestSlug + "-current\nimage_digest: sha256:deadbeef\n"
	pruneTestSlug       = "testslug"
)

// runPruneActiveVMTest sets up a state dir for pruneTestSlug (unless yaml is
// empty), runs pruneActiveVMHomeVolumes, and returns the client, UI, report,
// and error for the caller to assert on.
func runPruneActiveVMTest(
	t *testing.T,
	client *msb.MockMsbClient,
	homesBySlug map[string][]volumeWithAge,
	dryRun bool,
	yaml string,
) (*msb.MockMsbClient, *termio.Mock, *StaleReport, error) {
	t.Helper()
	ui := &termio.Mock{}
	report := &StaleReport{}

	configpaths.WithMockConfigPaths(t)

	if yaml != "" {
		overrideDir := filepath.Join(configpaths.Get().UserStateDir(), pruneTestSlug)
		os.MkdirAll(overrideDir, 0o700)
		testutil.WriteFile(t, overrideDir, "state.yaml", yaml)
	}

	err := pruneActiveVMHomeVolumes(
		context.Background(), client, pruneTestSlug, "sha256:deadbeef", homesBySlug, dryRun, ui, report,
	)
	return client, ui, report, err
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
		})
	}
}

func TestArtifactFor_ImageReferences(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "runner with digest tag",
			input:      "opencode-sandbox/runner-myproject:xYz1234AbCdEfGh",
			wantSlug:   "myproject",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "runner with latest tag",
			input:      "opencode-sandbox/runner-myproject:latest",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner with empty tag",
			input:      "opencode-sandbox/runner-myproject:",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner no tag at all",
			input:      "opencode-sandbox/runner-myproject",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner with complex slug",
			input:      "opencode-sandbox/runner-my-project-name:xYz1234AbCdEfGh",
			wantSlug:   "my-project-name",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "runner with multiple colons in tag (uses LastIndex)",
			input:      "opencode-sandbox/runner-myproject:sha256:abc123",
			wantSlug:   "myproject:sha256",
			wantDigest: "abc123",
		},
		{
			name:       "base image excluded-like prefix still parsed",
			input:      "opencode-sandbox/runner-base",
			wantSlug:   "base",
			wantDigest: "",
		},
		{name: "base image with tag", input: "opencode-sandbox/runner-base:latest", wantSlug: "base", wantDigest: ""},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_VMNames(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "simple vm name without hash suffix",
			input:      "opencode-sandbox-vm-projectname-main",
			wantSlug:   "projectname-main",
			wantDigest: "",
		},
		{
			name:       "slug with dash and branch with dash",
			input:      "opencode-sandbox-vm-my-project-feature-branch",
			wantSlug:   "my-project-feature-branch",
			wantDigest: "",
		},
		{
			name:       "single word slug with single word branch",
			input:      "opencode-sandbox-vm-myproject-main",
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
			input:      "opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			wantSlug:   "myproject-aB3cDe4fGhIjKl",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "home volume slug with dashes",
			input:      "opencode-sandbox-home-my-project-aB3cDe4fGhIjKl",
			wantSlug:   "my-project",
			wantDigest: "aB3cDe4fGhIjKl",
		},
		{
			name:       "home volume single part slug",
			input:      "opencode-sandbox-home-myproject-xYz1234",
			wantSlug:   "myproject",
			wantDigest: "xYz1234",
		},
		{
			name:       "home volume only two parts (slug-digest)",
			input:      "opencode-sandbox-home-proj-abc123",
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
			input:      "opencode-sandbox-task-prefill-proj-1719432000",
			wantSlug:   "prefill-proj",
			wantDigest: "",
		},
		{
			name:       "task sandbox single slug part before dash",
			input:      "opencode-sandbox-task-fill-proj",
			wantSlug:   "fill",
			wantDigest: "",
		},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_CloneVolumes(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "clone volume",
			input:      "opencode-sandbox-clone-proj-aBc1234D-1719432000",
			wantSlug:   "proj-aBc1234D",
			wantDigest: "",
		},
		{name: "clone volume minimal", input: "opencode-sandbox-clone-work-a1b2c3d4", wantSlug: "work", wantDigest: ""},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_VMWithHashSuffix(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "vm with 14-char hash suffix (no branch)",
			input:      "opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0",
			wantSlug:   "opencode-sandbox-1mjusbm3wikhb0",
			wantDigest: "",
		},
		{
			name:       "vm with 14-char hash and branch",
			input:      "opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0-main",
			wantSlug:   "opencode-sandbox-1mjusbm3wikhb0",
			wantDigest: "main",
		},
		{
			name:       "vm with 14-char hash, slug with dash, and branch",
			input:      "opencode-sandbox-vm-my-project-1mjusbm3wikhb0-develop",
			wantSlug:   "my-project-1mjusbm3wikhb0",
			wantDigest: "develop",
		},
		{
			name:       "user's case: VM without branch matches image slug",
			input:      "opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0",
			wantSlug:   "opencode-sandbox-1mjusbm3wikhb0",
			wantDigest: "",
		},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_UnrecognizedPrefixes(t *testing.T) {
	tests := []slugDigestTest{
		{name: "random string", input: "some-random-name", wantSlug: "", wantDigest: ""},
		{name: "empty string", input: "", wantSlug: "", wantDigest: ""},
		{name: "similar but wrong prefix", input: "opencode-sandbox-other-myslug", wantSlug: "", wantDigest: ""},
		{name: "just the prefix no remainder for vm", input: "opencode-sandbox-vm-", wantSlug: "", wantDigest: ""},
		{
			name:       "just the prefix home with single part after",
			input:      "opencode-sandbox-home-",
			wantSlug:   "",
			wantDigest: "",
		},
	}
	runSlugDigestTests(t, tests)
}

func TestArtifactFor_VMOnlyTwoParts(t *testing.T) {
	// VM with only two parts after prefix (e.g. name-branch, but we have name-branch).
	// "opencode-sandbox-vm-proj-main" → parts=["proj","main"] → slug = "proj".
	tests := []struct {
		name     string
		input    string
		wantSlug string
	}{
		{
			name:     "two parts vm",
			input:    "opencode-sandbox-vm-proj-main",
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

func TestFindStaleVMs(t *testing.T) {
	now := time.Now()
	oldTime := now.Add(-2 * time.Hour)
	recentTime := now.Add(-5 * time.Minute)
	threshold := 30 * time.Minute

	tests := []struct {
		name      string
		sandboxes []staleVM
		threshold time.Duration
		wantCount int
		wantNames []string
	}{
		{
			name:      "empty list",
			sandboxes: nil,
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "stopped VM past threshold",
			sandboxes: []staleVM{
				{name: "opencode-sandbox-vm-old-stale", status: msbSdk.SandboxStatusStopped, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 1,
			wantNames: []string{"opencode-sandbox-vm-old-stale"},
		},
		{
			name: "stopped VM not past threshold",
			sandboxes: []staleVM{
				{name: "opencode-sandbox-vm-recent", status: msbSdk.SandboxStatusStopped, updatedAt: recentTime},
			},
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "crashed VM past threshold",
			sandboxes: []staleVM{
				{name: "opencode-sandbox-vm-crashed-old", status: msbSdk.SandboxStatusCrashed, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 1,
			wantNames: []string{"opencode-sandbox-vm-crashed-old"},
		},
		{
			name: "running VM ignored even if old",
			sandboxes: []staleVM{
				{name: "opencode-sandbox-vm-running-old", status: msbSdk.SandboxStatusRunning, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "draining VM ignored",
			sandboxes: []staleVM{
				{name: "opencode-sandbox-vm-draining", status: msbSdk.SandboxStatusDraining, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "paused VM ignored",
			sandboxes: []staleVM{
				{name: "opencode-sandbox-vm-paused", status: msbSdk.SandboxStatusPaused, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "mixed statuses",
			sandboxes: []staleVM{
				{name: "opencode-sandbox-vm-old-stopped", status: msbSdk.SandboxStatusStopped, updatedAt: oldTime},
				{
					name:      "opencode-sandbox-vm-recent-stopped",
					status:    msbSdk.SandboxStatusStopped,
					updatedAt: recentTime,
				},
				{name: "opencode-sandbox-vm-old-running", status: msbSdk.SandboxStatusRunning, updatedAt: oldTime},
				{name: "opencode-sandbox-vm-old-crashed", status: msbSdk.SandboxStatusCrashed, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 2,
			wantNames: []string{"opencode-sandbox-vm-old-stopped", "opencode-sandbox-vm-old-crashed"},
		},
		{
			name: "zero threshold stops everything stopped",
			sandboxes: []staleVM{
				{name: "opencode-sandbox-vm-recent", status: msbSdk.SandboxStatusStopped, updatedAt: recentTime},
			},
			threshold: 0,
			wantCount: 1,
			wantNames: []string{"opencode-sandbox-vm-recent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findStaleVMs(tt.sandboxes, tt.threshold)

			if len(got) != tt.wantCount {
				t.Errorf("findStaleVMs() returned %d entries, want %d", len(got), tt.wantCount)
				for _, e := range got {
					t.Logf("  entry: %s (stale %s)", e.Name, e.StaleFor)
				}
				return
			}

			for i, name := range tt.wantNames {
				if got[i].Name != name {
					t.Errorf("entry[%d] name = %q, want %q", i, got[i].Name, name)
				}
				if got[i].Type != StaleTypeVM {
					t.Errorf("entry[%d] type = %q, want %q", i, got[i].Type, "vm")
				}
				if got[i].StaleFor <= 0 {
					t.Errorf("entry[%d] stalefor = %v, want > 0", i, got[i].StaleFor)
				}
			}
		})
	}
}

func TestFindStaleVMs_StaleEntryFields(t *testing.T) {
	now := time.Now()
	sandboxes := []staleVM{
		{name: "test-vm", status: msbSdk.SandboxStatusStopped, updatedAt: now.Add(-1 * time.Hour)},
	}
	threshold := 30 * time.Minute

	got := findStaleVMs(sandboxes, threshold)
	if len(got) != 1 {
		t.Fatalf("expected 1 stale VM, got %d", len(got))
	}

	entry := got[0]
	if entry.Type != StaleTypeVM {
		t.Errorf("Type = %q, want %q", entry.Type, "vm")
	}
	if entry.Name != "test-vm" {
		t.Errorf("Name = %q, want %q", entry.Name, "test-vm")
	}
	if entry.StaleFor < 29*time.Minute {
		t.Errorf("StaleFor = %v, expected at least 30m", entry.StaleFor)
	}
}

func TestFindStaleVMs_NilInput(t *testing.T) {
	got := findStaleVMs(nil, 1*time.Hour)
	if got != nil {
		t.Errorf("findStaleVMs(nil) returned non-nil: %v", got)
	}
}

func TestStaleReport(t *testing.T) {
	report := &StaleReport{
		PrunedVMs:           3,
		PrunedVolumes:       5,
		PrunedDockerImages:  2,
		PrunedMSBImages:     0,
		PrunedTaskSandboxes: 1,
		PrunedCloneVolumes:  0,
		Details: []StaleEntry{
			{Type: StaleTypeVM, Name: "vm1", StaleFor: 2 * time.Hour},
			{Type: StaleTypeVolume, Name: "vol1", StaleFor: 3 * time.Hour},
		},
	}

	if report.PrunedVMs != 3 {
		t.Errorf("PrunedVMs = %d, want 3", report.PrunedVMs)
	}
	if report.PrunedVolumes != 5 {
		t.Errorf("PrunedVolumes = %d, want 5", report.PrunedVolumes)
	}
	if report.PrunedTaskSandboxes != 1 {
		t.Errorf("PrunedTaskSandboxes = %d, want 1", report.PrunedTaskSandboxes)
	}
	if len(report.Details) != 2 {
		t.Errorf("Details length = %d, want 2", len(report.Details))
	}

	if report.Details[0].Type != StaleTypeVM {
		t.Errorf("Details[0].Type = %q, expected vm", report.Details[0].Type)
	}
}

func TestArtifactFor_ComplexSlugNames(t *testing.T) {
	tests := []slugDigestTest{
		{
			name:       "home with long slug containing hashes",
			input:      "opencode-sandbox-home-abcdef-gH1234AB5678CD-eF9012gH3456iJ",
			wantSlug:   "abcdef-gH1234AB5678CD",
			wantDigest: "eF9012gH3456iJ",
		},
		{
			name:       "vm with two-part name",
			input:      "opencode-sandbox-vm-acme-corp",
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
		{"opencode-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{"opencode-sandbox/runner-myproject:latest", "myproject", ""},
		{"opencode-sandbox/runner-myproject:", "myproject", ""},
		{"opencode-sandbox/runner-myproject", "myproject", ""},
		{"opencode-sandbox/runner-my-project-name:xYz1234AbCdEfGh", "my-project-name", "xYz1234AbCdEfGh"},
		{"opencode-sandbox/runner-myproject:sha256:abc123", "myproject:sha256", "abc123"},
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
	}{
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0", "opencode-sandbox-1mjusbm3wikhb0", ""},
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0-main", "opencode-sandbox-1mjusbm3wikhb0", "main"},
		{"opencode-sandbox-vm-my-project-1mjusbm3wikhb0-develop", "my-project-1mjusbm3wikhb0", "develop"},
		{"opencode-sandbox-vm-projectname-main", "projectname-main", ""},
		{"opencode-sandbox-vm-myproject-abc1234567890", "myproject-abc1234567890", ""},
		{"opencode-sandbox-vm-noHash", "noHash", ""},
		{"opencode-sandbox-home-test", "", ""},
		{"opencode-sandbox-vm-projectname-aB3cDe4fGhIjKl", "projectname-aB3cDe4fGhIjKl", ""},
		{"opencode-sandbox-vm-projectname-aB3cDe4fGhIjKl-feature", "projectname-aB3cDe4fGhIjKl-feature", ""},
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
			"opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			"myproject-aB3cDe4fGhIjKl",
			"xYz1234AbCdEfGh",
		},
		{"opencode-sandbox-home-myproject-abc1234567890", "myproject", "abc1234567890"},
		{"opencode-sandbox-home-abc-def-gh", "abc-def", "gh"},
		{"opencode-sandbox-vm-something", "", ""},
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
		{"opencode-sandbox-home-myproj-20260806T143022", "myproj", ""},
		{"opencode-sandbox-home-abc-def-20260806T143022", "abc-def", ""},
		{"opencode-sandbox-home-proj-20261231T235959", "proj", ""},
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
	info := naming.ParseHomeVolumeName("opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh")
	if info.Slug != "myproject-aB3cDe4fGhIjKl" {
		t.Errorf("slug = %q, want %q", info.Slug, "myproject-aB3cDe4fGhIjKl")
	}
	if info.Digest != "xYz1234AbCdEfGh" {
		t.Errorf("digest = %q, want %q", info.Digest, "xYz1234AbCdEfGh")
	}
}

func TestParseCloneVolumeName(t *testing.T) {
	tests := []struct {
		input    string
		wantSlug string
	}{
		{"opencode-sandbox-clone-proj-aBc1234D-1719432000", "proj-aBc1234D"},
		{"opencode-sandbox-clone-my-project-something", "my-project"},
		{"opencode-sandbox-home-foo", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			slug := naming.ParseCloneVolumeName(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("naming.ParseCloneVolumeName(%q) = %q, want %q", tt.input, slug, tt.wantSlug)
			}
		})
	}
}

func TestRemoveHomeVolumes_CleansStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{}
	ui := &termio.Mock{}

	report := &StaleReport{}

	slug := "myproject"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-sandbox-home-myproject-20260806T143022")},
	}

	statePath := filepath.Join(configpaths.Get().UserStateDir(), slug, "state.yaml")
	os.MkdirAll(filepath.Dir(statePath), 0o700)
	testutil.WritePath(t, statePath,
		"home_volume: opencode-sandbox-home-myproject-20260806T143022\nimage_digest: sha256:abc\n")

	removeHomeVolumes(context.Background(), client, slug, pruneThreshold, homeBySlugDigest, false, ui, report)

	if report.PrunedVolumes != 1 {
		t.Errorf("expected 1 pruned volume, got %d", report.PrunedVolumes)
	}
	if len(client.RemovedVolumes) != 1 {
		t.Errorf("expected 1 volume removal, got %d", len(client.RemovedVolumes))
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state file should be removed, still exists at %s", statePath)
	}
}

func TestRemoveHomeVolumes_DryRunDoesNotRemoveState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{}
	ui := &termio.Mock{}
	report := &StaleReport{}

	slug := "myproject"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-sandbox-home-myproject-20260806T143022")},
	}

	statePath := filepath.Join(configpaths.Get().UserStateDir(), slug, "state.yaml")
	os.MkdirAll(filepath.Dir(statePath), 0o700)
	testutil.WritePath(t, statePath,
		"home_volume: opencode-sandbox-home-myproject-20260806T143022\n")

	removeHomeVolumes(context.Background(), client, slug, pruneThreshold, homeBySlugDigest, true, ui, report)

	if report.PrunedVolumes != 1 {
		t.Errorf("expected 1 pruned volume in dry-run, got %d", report.PrunedVolumes)
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("expected no volume removals in dry-run, got %d", len(client.RemovedVolumes))
	}
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Errorf("state file should still exist in dry-run")
	}
}

func TestPruneActiveVMHomeVolumes_KeepsStateVolume(t *testing.T) {
	homesBySlug := map[string][]volumeWithAge{
		pruneTestSlug: {oldVol("opencode-sandbox-home-testslug-current"), oldVol("opencode-sandbox-home-testslug-old")},
	}

	client, _, report, err := runPruneActiveVMTest(
		t, &msb.MockMsbClient{}, homesBySlug, false, homeVolumeStateYAML,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.PrunedVolumes != 1 {
		t.Errorf("expected 1 pruned volume, got %d", report.PrunedVolumes)
	}
	if len(client.RemovedVolumes) != 1 || client.RemovedVolumes[0] != "opencode-sandbox-home-testslug-old" {
		t.Errorf("removed volumes = %v, want [opencode-sandbox-home-testslug-old]", client.RemovedVolumes)
	}
}

func TestPruneActiveVMHomeVolumes_RemovesAllWhenStateVolumeAbsent(t *testing.T) {
	homesBySlug := map[string][]volumeWithAge{
		pruneTestSlug: {oldVol("opencode-sandbox-home-testslug-old1"), oldVol("opencode-sandbox-home-testslug-old2")},
	}

	client, _, report, err := runPruneActiveVMTest(
		t, &msb.MockMsbClient{}, homesBySlug, false, homeVolumeStateYAML,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.PrunedVolumes != 2 {
		t.Errorf("expected 2 pruned volumes, got %d", report.PrunedVolumes)
	}
	if len(client.RemovedVolumes) != 2 {
		t.Errorf("removed volumes = %v, want 2 removals", client.RemovedVolumes)
	}
}

func TestPruneActiveVMHomeVolumes_DryRunCountsButDoesNotDelete(t *testing.T) {
	homesBySlug := map[string][]volumeWithAge{
		pruneTestSlug: {oldVol("opencode-sandbox-home-testslug-current"), oldVol("opencode-sandbox-home-testslug-old")},
	}

	client, _, report, err := runPruneActiveVMTest(
		t, &msb.MockMsbClient{}, homesBySlug, true, homeVolumeStateYAML,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.PrunedVolumes != 1 {
		t.Errorf("expected 1 pruned volume in dry-run, got %d", report.PrunedVolumes)
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("expected no volume removals in dry-run, got %v", client.RemovedVolumes)
	}
}

func TestPruneActiveVMHomeVolumes_MissingStateFileReturnsError(t *testing.T) {
	homesBySlug := map[string][]volumeWithAge{
		pruneTestSlug: {oldVol("opencode-sandbox-home-testslug-old")},
	}

	_, _, report, err := runPruneActiveVMTest(t, &msb.MockMsbClient{}, homesBySlug, false, "")
	if err == nil {
		t.Fatal("expected error for missing state file, got nil")
	}
	if report.PrunedVolumes != 0 {
		t.Errorf("expected no pruned volumes when state missing, got %d", report.PrunedVolumes)
	}
}

func TestPruneActiveVMHomeVolumes_RemoveErrorWarns(t *testing.T) {
	homesBySlug := map[string][]volumeWithAge{
		pruneTestSlug: {oldVol("opencode-sandbox-home-testslug-current"), oldVol("opencode-sandbox-home-testslug-old")},
	}

	client := &msb.MockMsbClient{
		RemoveVolumeFn: func(_ context.Context, _ string) error { return errors.New("volume busy") },
	}
	_, ui, report, err := runPruneActiveVMTest(t, client, homesBySlug, false, homeVolumeStateYAML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.WarnCalls) != 1 {
		t.Fatalf("expected 1 warning on volume removal failure, got %d: %v", len(ui.WarnCalls), ui.WarnCalls)
	}
	if report.PrunedVolumes != 0 {
		t.Errorf("expected no pruned volumes on removal failure, got %d", report.PrunedVolumes)
	}
}

func TestPruneDockerImagesFailureIsWarn(t *testing.T) {
	dockerMock := &mockDockerClient{pruneErr: errors.New("prune failed")}
	docker.WithDockerMock(t, dockerMock)
	report := &StaleReport{}
	ui := newMockUI()

	pruneDockerImages(context.Background(), false, ui, report)

	if report.PrunedDockerImages != 0 {
		t.Errorf("PrunedDockerImages = %d, want 0", report.PrunedDockerImages)
	}
	if !dockerMock.pruneCalled {
		t.Error("expected ImagePrune to be called")
	}
	found := false
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, "failed to prune docker images") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a Warn call about failed docker image prune, WarnCalls = %v", ui.WarnCalls)
	}
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "failed to prune docker images") {
			t.Errorf("failed docker image prune should not be Verbose, VerboseCalls = %v", ui.VerboseCalls)
		}
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

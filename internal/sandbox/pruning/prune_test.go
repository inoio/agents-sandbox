package pruning

import (
	"context"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	cp "gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

type slugDigestTest struct {
	name       string
	input      string
	wantSlug   string
	wantDigest string
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

func TestPruneAggregateParity(t *testing.T) {
	old := time.Now().Add(-15 * 24 * time.Hour)
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-proj-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: old,
			},
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-live-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: old,
				Image_:     "opencode-sandbox/runner-live-1mjusbm3wikhb0:cur",
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "opencode-sandbox-home-proj-1mjusbm3wikhb0-20260806T143022", CreatedAt_: old},
			&msb.MockVolumeHandle{Name_: "opencode-sandbox-home-live-1mjusbm3wikhb0-20260806T143022", CreatedAt_: old},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-proj-1mjusbm3wikhb0:old", LastUsedAt_: old},
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-live-1mjusbm3wikhb0:cur", LastUsedAt_: old},
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-live-1mjusbm3wikhb0:old", LastUsedAt_: old},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	cp.WithMockConfigPaths(t)

	testUI := termio.NewTestMock(t)
	if err := Prune(context.Background(), 7*24*time.Hour, false, false, &testUI); err != nil {
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

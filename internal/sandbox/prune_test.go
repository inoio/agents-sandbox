package sandbox

import (
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestExtractProjectSlugAndDigest_ImageReferences(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			name:       "runner with digest tag",
			input:      "opencode-msb/runner-myproject:xYz1234AbCdEfGh",
			wantSlug:   "myproject",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "runner with latest tag",
			input:      "opencode-msb/runner-myproject:latest",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner with empty tag",
			input:      "opencode-msb/runner-myproject:",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner no tag at all",
			input:      "opencode-msb/runner-myproject",
			wantSlug:   "myproject",
			wantDigest: "",
		},
		{
			name:       "runner with complex slug",
			input:      "opencode-msb/runner-my-project-name:xYz1234AbCdEfGh",
			wantSlug:   "my-project-name",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "runner with multiple colons in tag (uses LastIndex)",
			input:      "opencode-msb/runner-myproject:sha256:abc123",
			wantSlug:   "myproject:sha256",
			wantDigest: "abc123",
		},
		{
			name:       "base image excluded-like prefix still parsed",
			input:      "opencode-msb/runner-base",
			wantSlug:   "base",
			wantDigest: "",
		},
		{
			name:       "base image with tag",
			input:      "opencode-msb/runner-base:latest",
			wantSlug:   "base",
			wantDigest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, digest := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("extractProjectSlugAndDigest(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestExtractProjectSlugAndDigest_VMNames(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			name:       "simple vm name without hash suffix",
			input:      "opencode-msb-vm-projectname-main",
			wantSlug:   "projectname-main",
			wantDigest: "",
		},
		{
			name:       "slug with dash and branch with dash",
			input:      "opencode-msb-vm-my-project-feature-branch",
			wantSlug:   "my-project-feature-branch",
			wantDigest: "",
		},
		{
			name:       "single word slug with single word branch",
			input:      "opencode-msb-vm-myproject-main",
			wantSlug:   "myproject-main",
			wantDigest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, digest := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("extractProjectSlugAndDigest(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestExtractProjectSlugAndDigest_HomeVolumes(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			name:       "home volume with slug and digest",
			input:      "opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			wantSlug:   "myproject-aB3cDe4fGhIjKl",
			wantDigest: "xYz1234AbCdEfGh",
		},
		{
			name:       "home volume slug with dashes",
			input:      "opencode-msb-home-my-project-aB3cDe4fGhIjKl",
			wantSlug:   "my-project",
			wantDigest: "aB3cDe4fGhIjKl",
		},
		{
			name:       "home volume single part slug",
			input:      "opencode-msb-home-myproject-xYz1234",
			wantSlug:   "myproject",
			wantDigest: "xYz1234",
		},
		{
			name:       "home volume only two parts (slug-digest)",
			input:      "opencode-msb-home-proj-abc123",
			wantSlug:   "proj",
			wantDigest: "abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, digest := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("extractProjectSlugAndDigest(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestExtractProjectSlugAndDigest_TaskSandboxes(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			name:       "task sandbox",
			input:      "opencode-msb-task-prefill-proj-1719432000",
			wantSlug:   "prefill-proj",
			wantDigest: "",
		},
		{
			name:       "task sandbox single slug part before dash",
			input:      "opencode-msb-task-fill-proj",
			wantSlug:   "fill",
			wantDigest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, digest := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("extractProjectSlugAndDigest(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestExtractProjectSlugAndDigest_CloneVolumes(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			name:       "clone volume",
			input:      "opencode-msb-clone-proj-aBc1234D-1719432000",
			wantSlug:   "proj-aBc1234D",
			wantDigest: "",
		},
		{
			name:       "clone volume minimal",
			input:      "opencode-msb-clone-work-a1b2c3d4",
			wantSlug:   "work",
			wantDigest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, digest := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("extractProjectSlugAndDigest(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestExtractProjectSlugAndDigest_VMWithHashSuffix(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			name:       "vm with 14-char hash suffix (no branch)",
			input:      "opencode-msb-vm-saife-1mjusbm3wikhb0",
			wantSlug:   "saife-1mjusbm3wikhb0",
			wantDigest: "",
		},
		{
			name:       "vm with 14-char hash and branch",
			input:      "opencode-msb-vm-saife-1mjusbm3wikhb0-main",
			wantSlug:   "saife-1mjusbm3wikhb0",
			wantDigest: "main",
		},
		{
			name:       "vm with 14-char hash, slug with dash, and branch",
			input:      "opencode-msb-vm-my-project-1mjusbm3wikhb0-develop",
			wantSlug:   "my-project-1mjusbm3wikhb0",
			wantDigest: "develop",
		},
		{
			name:       "user's case: VM without branch matches image slug",
			input:      "opencode-msb-vm-saife-1mjusbm3wikhb0",
			wantSlug:   "saife-1mjusbm3wikhb0",
			wantDigest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, digest := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("extractProjectSlugAndDigest(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestFindHashSuffix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			name:  "no hash - simple name",
			input: "projectname-main",
			want:  -1,
		},
		{
			name:  "hash at hyphen position 5",
			input: "saife-1mjusbm3wikhb0",
			want:  6,
		},
		{
			name:  "hash followed by branch",
			input: "saife-1mjusbm3wikhb0-main",
			want:  6,
		},
		{
			name:  "hash embedded in multi-dash slug",
			input: "my-project-1mjusbm3wikhb0-develop",
			want:  11,
		},
		{
			name:  "no hash - short string",
			input: "abc-def",
			want:  -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findHashSuffix(tt.input)
			if got != tt.want {
				t.Errorf("findHashSuffix(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractProjectSlugAndDigest_UnrecognizedPrefixes(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			name:       "random string",
			input:      "some-random-name",
			wantSlug:   "",
			wantDigest: "",
		},
		{
			name:       "empty string",
			input:      "",
			wantSlug:   "",
			wantDigest: "",
		},
		{
			name:       "similar but wrong prefix",
			input:      "opencode-msb-other-myslug",
			wantSlug:   "",
			wantDigest: "",
		},
		{
			name:       "just the prefix no remainder for vm",
			input:      "opencode-msb-vm-",
			wantSlug:   "",
			wantDigest: "",
		},
		{
			name:       "just the prefix home with single part after",
			input:      "opencode-msb-home-",
			wantSlug:   "",
			wantDigest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, digest := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("extractProjectSlugAndDigest(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestExtractProjectSlugAndDigest_VMOnlyTwoParts(t *testing.T) {
	// VM with only two parts after prefix (e.g. name-branch, but we have name-branch).
	// "opencode-msb-vm-proj-main" → parts=["proj","main"] → slug = "proj".
	tests := []struct {
		name     string
		input    string
		wantSlug string
	}{
		{
			name:     "two parts vm",
			input:    "opencode-msb-vm-proj-main",
			wantSlug: "proj-main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, _ := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
		})
	}
}

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
		{"empty string", msb.SandboxStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStoppedStatus(tt.status)
			if got != tt.want {
				t.Errorf("isStoppedStatus(%q) = %v, want %v", tt.status, got, tt.want)
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
				{name: "opencode-msb-vm-old-stale", status: msb.SandboxStatusStopped, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 1,
			wantNames: []string{"opencode-msb-vm-old-stale"},
		},
		{
			name: "stopped VM not past threshold",
			sandboxes: []staleVM{
				{name: "opencode-msb-vm-recent", status: msb.SandboxStatusStopped, updatedAt: recentTime},
			},
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "crashed VM past threshold",
			sandboxes: []staleVM{
				{name: "opencode-msb-vm-crashed-old", status: msb.SandboxStatusCrashed, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 1,
			wantNames: []string{"opencode-msb-vm-crashed-old"},
		},
		{
			name: "running VM ignored even if old",
			sandboxes: []staleVM{
				{name: "opencode-msb-vm-running-old", status: msb.SandboxStatusRunning, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "draining VM ignored",
			sandboxes: []staleVM{
				{name: "opencode-msb-vm-draining", status: msb.SandboxStatusDraining, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "paused VM ignored",
			sandboxes: []staleVM{
				{name: "opencode-msb-vm-paused", status: msb.SandboxStatusPaused, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 0,
			wantNames: nil,
		},
		{
			name: "mixed statuses",
			sandboxes: []staleVM{
				{name: "opencode-msb-vm-old-stopped", status: msb.SandboxStatusStopped, updatedAt: oldTime},
				{name: "opencode-msb-vm-recent-stopped", status: msb.SandboxStatusStopped, updatedAt: recentTime},
				{name: "opencode-msb-vm-old-running", status: msb.SandboxStatusRunning, updatedAt: oldTime},
				{name: "opencode-msb-vm-old-crashed", status: msb.SandboxStatusCrashed, updatedAt: oldTime},
			},
			threshold: threshold,
			wantCount: 2,
			wantNames: []string{"opencode-msb-vm-old-stopped", "opencode-msb-vm-old-crashed"},
		},
		{
			name: "zero threshold stops everything stopped",
			sandboxes: []staleVM{
				{name: "opencode-msb-vm-recent", status: msb.SandboxStatusStopped, updatedAt: recentTime},
			},
			threshold: 0,
			wantCount: 1,
			wantNames: []string{"opencode-msb-vm-recent"},
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
		{name: "test-vm", status: msb.SandboxStatusStopped, updatedAt: now.Add(-1 * time.Hour)},
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

func TestExtractProjectSlugAndDigest_ComplexSlugNames(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSlug   string
		wantDigest string
	}{
		{
			name:       "home with long slug containing hashes",
			input:      "opencode-msb-home-abcdef-gH1234AB5678CD-eF9012gH3456iJ",
			wantSlug:   "abcdef-gH1234AB5678CD",
			wantDigest: "eF9012gH3456iJ",
		},
		{
			name:       "vm with two-part name",
			input:      "opencode-msb-vm-acme-corp",
			wantSlug:   "acme-corp",
			wantDigest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, digest := extractProjectSlugAndDigest(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("extractProjectSlugAndDigest(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("extractProjectSlugAndDigest(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

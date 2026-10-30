package git

import (
	"strings"
	"testing"
)

func TestHashIDReturns14Chars(t *testing.T) {
	got := HashID("test-input")
	if len(got) != 14 {
		t.Errorf("expected 14 chars, got %d (%q)", len(got), got)
	}
}

func TestHashIDDeterministic(t *testing.T) {
	a := HashID("sha256:abc123def456")
	b := HashID("sha256:abc123def456")
	if a != b {
		t.Errorf("expected deterministic output, got %q and %q", a, b)
	}
}

func TestHashIDDifferentInputs(t *testing.T) {
	a := HashID("hello")
	b := HashID("world")
	if a == b {
		t.Errorf("expected different hashes for different inputs, got %q for both", a)
	}
}

func TestHashIDBase36AlphabetOnly(t *testing.T) {
	got := HashID("sha256:fce5c4a3b2d1e0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5")
	for _, r := range got {
		isBase36 := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')
		if !isBase36 {
			t.Errorf("expected base36 alphabet only, found %q in %q", r, got)
		}
	}
}

func TestHashIDKnownValues(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "5oaq0bjhj6un82"},
		{"sha256:abc123def456", "3k5q07ywpibwp5"},
		{"hello", "14bu24ea7cq4jh"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := HashID(tt.input); got != tt.want {
				t.Errorf("HashID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeFolderNameLowercases(t *testing.T) {
	got := sanitizeFolderName("MyApp")
	if got != "myapp" {
		t.Errorf("expected %q, got %q", "myapp", got)
	}
}

func TestSanitizeFolderNameReplacesNonAlnum(t *testing.T) {
	got := sanitizeFolderName("My App")
	if got != "my-app" {
		t.Errorf("expected %q, got %q", "my-app", got)
	}
}

func TestSanitizeFolderNameCollapsesDashes(t *testing.T) {
	got := sanitizeFolderName("My---App!!!")
	if got != "my-app" {
		t.Errorf("expected %q, got %q", "my-app", got)
	}
}

func TestSanitizeFolderNameTrimsLeadingTrailingDashes(t *testing.T) {
	got := sanitizeFolderName("---leading-and-trailing---")
	if got != "leading-and-trailing" {
		t.Errorf("expected %q, got %q", "leading-and-trailing", got)
	}
}

func TestSanitizeFolderNameCapsAt20(t *testing.T) {
	got := sanitizeFolderName("abcdefghijklmnopqrstuvwxyz")
	if len(got) > 20 {
		t.Errorf("expected <= 20 chars, got %d (%q)", len(got), got)
	}
}

func TestSanitizeFolderNameEmptyInput(t *testing.T) {
	got := sanitizeFolderName("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestProjectSlugFallsBackWhenFolderNameEmpty(t *testing.T) {
	got := projectSlug("", "some-id")
	if !strings.HasPrefix(got, "project-") {
		t.Errorf("expected slug to start with 'project-', got %q", got)
	}
	if len(got) != len("project-")+14 {
		t.Errorf("expected slug of %d chars, got %d (%q)", len("project-")+14, len(got), got)
	}
}

func TestProjectSlugUsesSanitizedFolderName(t *testing.T) {
	got := projectSlug("my-app", "some-id")
	if !strings.HasPrefix(got, "my-app-") {
		t.Errorf("expected slug to start with 'my-app-', got %q", got)
	}
	if len(got) != len("my-app-")+14 {
		t.Errorf("expected slug of %d chars, got %d (%q)", len("my-app-")+14, len(got), got)
	}
}

func TestLastPathSegment(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"https with .git", "https://gitlab.example.com/org/repo.git", "repo"},
		{"https no .git", "https://gitlab.example.com/org/my-repo", "my-repo"},
		{"ssh scp-like with namespace", "git@github.com:inoio/agents-sandbox.git", "agents-sandbox"},
		{"ssh scp-like no namespace", "git@gitlab.example.com:tool.git", "tool"},
		{"git protocol", "git://gitlab.example.com/org/repo.git", "repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastPathSegment(tt.url); got != tt.want {
				t.Errorf("lastPathSegment(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

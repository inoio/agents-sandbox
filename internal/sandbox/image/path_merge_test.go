package image

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// runMergeScript sources data/agents-sandbox-path.sh in a fresh POSIX shell
// with the given environment and returns the resulting PATH. A source count > 1
// exercises re-sourcing (nested login shells).
func runMergeScript(t *testing.T, path, imagePath string, setImage bool, sources int) string {
	t.Helper()
	script, err := filepath.Abs("data/agents-sandbox-path.sh")
	if err != nil {
		t.Fatalf("abs script path: %v", err)
	}
	shell := ". " + script
	if sources > 1 {
		shell += "; . " + script
	}
	shell += `; printf '%s' "$PATH"`

	cmd := exec.Command("sh", "-c", shell)
	cmd.Env = []string{"PATH=" + path}
	if setImage {
		cmd.Env = append(cmd.Env, "AGENTS_SANDBOX_IMAGE_PATH="+imagePath)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh merge script: %v", err)
	}
	return string(out)
}

func TestAgentsSandboxPathMergeScript(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		imagePath string
		setImage  bool
		sources   int
		want      string
	}{
		{
			name:      "appends missing image entries in image order",
			path:      "/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin:/usr/local/go/bin",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin:/usr/bin:/opt/agents-sandbox/bin:/usr/local/go/bin",
		},
		{
			name:      "preserves login additions and their order",
			path:      "/.msb/scripts:/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/go/bin:/usr/local/bin",
			setImage:  true,
			sources:   1,
			want:      "/.msb/scripts:/usr/local/bin:/usr/bin:/usr/local/go/bin",
		},
		{
			name:      "adds nothing when all entries present",
			path:      "/usr/local/bin:/opt/agents-sandbox/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin:/opt/agents-sandbox/bin",
		},
		{
			name:      "prefix of an existing entry is still appended",
			path:      "/opt/agents-sandbox/bin",
			imagePath: "/opt",
			setImage:  true,
			sources:   1,
			want:      "/opt/agents-sandbox/bin:/opt",
		},
		{
			name:     "no-op when image path is unset",
			path:     "/usr/local/bin:/usr/bin",
			setImage: false,
			sources:  1,
			want:     "/usr/local/bin:/usr/bin",
		},
		{
			name:      "no-op when image path is empty",
			path:      "/usr/local/bin",
			imagePath: "",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin",
		},
		{
			name:      "idempotent when sourced twice",
			path:      "/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin",
			setImage:  true,
			sources:   2,
			want:      "/usr/local/bin:/usr/bin:/opt/agents-sandbox/bin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runMergeScript(t, tc.path, tc.imagePath, tc.setImage, tc.sources)
			if got != tc.want {
				t.Errorf("PATH = %q, want %q", got, tc.want)
			}
		})
	}
}

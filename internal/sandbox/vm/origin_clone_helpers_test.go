package vm

import (
	"os"
	"path/filepath"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// cloneOrigin is the shared origin URL for the identity fixtures, so two clones
// map to one repo slug while keeping distinct checkout slugs.
const cloneOrigin = "https://example.com/acme/widget.git"

// newOriginClone builds a minimal origin-holding repo layout (a .git dir whose
// config names the shared origin), the same fixture shape used by internal/git's
// identity tests. It returns the symlink-resolved path so assertions match what
// ResolveIdentity derives.
func newOriginClone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	config := "[remote \"origin\"]\n\turl = " + cloneOrigin + "\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	return root
}

// createdProjectVMConfig applies the recorded CreateSandbox options for the
// named project VM and returns the config it was created with. Other
// CreateSandbox calls (a home-volume prefill) are ignored.
func createdProjectVMConfig(t *testing.T, mock *msb.MockMsbClient, name string) msbSdk.SandboxConfig {
	t.Helper()
	for _, call := range mock.CreatedSandboxCalls {
		if call.Name != name {
			continue
		}
		var cfg msbSdk.SandboxConfig
		for _, opt := range call.Opts {
			opt(&cfg)
		}
		return cfg
	}
	t.Fatalf("no CreateSandbox call for %q; calls: %v", name, mock.CreatedSandboxes)
	return msbSdk.SandboxConfig{}
}

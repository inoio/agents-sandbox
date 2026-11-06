package envsecret

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestParseSecretSpecYAMLRejectsOversizedFile pins that a project secret file
// larger than the cap yields no specs (the read is refused, not parsed).
func TestParseSecretSpecYAMLRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret.yaml")
	testutil.WritePath(t, path, "FOO: bar@host\n# "+strings.Repeat("x", configread.MaxBytes)+"\n")
	ui := termio.NewTestMock(t)

	if specs := ParseSecretSpecYAML(path, &ui); specs != nil {
		t.Errorf("specs = %v, want nil for an oversized file", specs)
	}
}

// TestBuildEnvMapRejectsOversizedFile pins the same bound on the env file read.
func TestBuildEnvMapRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, path, "FOO=bar\n# "+strings.Repeat("x", configread.MaxBytes)+"\n")
	ui := termio.NewTestMock(t)

	if env := BuildEnvMap(path, &ui); env != nil {
		t.Errorf("env = %v, want nil for an oversized file", env)
	}
}

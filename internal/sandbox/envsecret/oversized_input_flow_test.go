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
// larger than the cap yields no specs (the read is refused, not parsed) and
// warns, so a present-but-oversized file is not dropped silently.
func TestParseSecretSpecYAMLRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret.yaml")
	testutil.WritePath(t, path, "FOO: bar@host\n# "+strings.Repeat("x", configread.MaxBytes)+"\n")
	ui := termio.NewTestMock(t)

	if specs := ParseSecretSpecYAML(path, &ui); specs != nil {
		t.Errorf("specs = %v, want nil for an oversized file", specs)
	}
	assertOversizeWarned(t, ui.WarnCalls)
}

// TestParseSecretSpecLegacyRejectsOversizedFile pins the same bound and warning
// on the legacy secret file read.
func TestParseSecretSpecLegacyRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "FOO=bar@host\n# "+strings.Repeat("x", configread.MaxBytes)+"\n")
	ui := termio.NewTestMock(t)

	if specs := ParseSecretSpecLegacy(path, &ui); specs != nil {
		t.Errorf("specs = %v, want nil for an oversized file", specs)
	}
	assertOversizeWarned(t, ui.WarnCalls)
}

// TestBuildEnvMapRejectsOversizedFile pins the same bound on the env file read.
func TestBuildEnvMapRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, path, "FOO=bar\n# "+strings.Repeat("x", configread.MaxBytes)+"\n")
	ui := termio.NewTestMock(t)

	if env := BuildEnvMap(path, &ui); env != nil {
		t.Errorf("env = %v, want nil for an oversized file", env)
	}
	assertOversizeWarned(t, ui.WarnCalls)
}

// TestBuildEnvMapStaysQuietForAMissingFile pins that an absent optional file is
// not warned about: only a present file the cap rejects is.
func TestBuildEnvMapStaysQuietForAMissingFile(t *testing.T) {
	ui := termio.NewTestMock(t)

	if env := BuildEnvMap(filepath.Join(t.TempDir(), "absent"), &ui); env != nil {
		t.Errorf("env = %v, want nil for a missing file", env)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none for a missing file", ui.WarnCalls)
	}
}

// assertOversizeWarned fails unless a recorded warning names the size cap, so a
// present-but-oversized file is surfaced rather than dropped silently.
func assertOversizeWarned(t *testing.T, warnings []string) {
	t.Helper()
	for _, warning := range warnings {
		if strings.Contains(warning, configread.ErrTooLarge.Error()) {
			return
		}
	}
	t.Errorf("warnings = %v, want one naming %q", warnings, configread.ErrTooLarge.Error())
}

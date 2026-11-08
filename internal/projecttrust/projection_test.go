package projecttrust

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// projectFixture is a checkout with its .agents-sandbox project config dir.
type projectFixture struct {
	checkout   string
	projectDir string
}

func newProjectFixture(t *testing.T) projectFixture {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	checkout := t.TempDir()
	return projectFixture{checkout: checkout, projectDir: filepath.Join(checkout, ".agents-sandbox")}
}

func (f projectFixture) write(t *testing.T, rel, content string) string {
	t.Helper()
	path := filepath.Join(f.projectDir, rel)
	writeFile(t, path, content)
	return path
}

// writeFile writes content to path, creating its parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, path, content)
}

func (f projectFixture) compute(t *testing.T) Projection {
	t.Helper()
	projection, err := Compute(Sources{
		ProjectConfigDir:    f.projectDir,
		CheckoutRoot:        f.checkout,
		AgentConfigDirNames: []string{"opencode", "opencode2"},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return projection
}

func inputAt(t *testing.T, projection Projection, path string) Input {
	t.Helper()
	for _, input := range projection.Inputs {
		if input.Path == path {
			return input
		}
	}
	t.Fatalf("projection %+v has no input %q", projection.Inputs, path)
	return Input{}
}

func TestComputeEmptyProjectLayer(t *testing.T) {
	f := newProjectFixture(t)
	if got, err := Compute(Sources{}); err != nil || !got.Empty() {
		t.Errorf("Compute(no project config dir) = %+v, %v; want empty", got, err)
	}
	if got := f.compute(t); !got.Empty() {
		t.Errorf("Compute(no project dir) = %+v, want empty", got)
	}
	if err := os.MkdirAll(f.projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if got := f.compute(t); !got.Empty() {
		t.Errorf("Compute(empty project dir) = %+v, want empty", got)
	}
}

// TestComputeLauncherConfigIsOneEntryPerKey pins the launcher config effect:
// every key the file sets, flattened and sorted, with a canonical value, so a
// reorder or reformat keeps the digest while any value change alters it.
func TestComputeLauncherConfigIsOneEntryPerKey(t *testing.T) {
	f := newProjectFixture(t)
	path := f.write(t, "config.yaml", "network:\n  profile: public\nmemory: 8G\nmounts:\n  /host: /\n")
	got := inputAt(t, f.compute(t), path)
	want := Input{Path: path, Kind: KindLauncherConfig, Entries: []string{
		`memory: "8G"`, `mounts./host: "/"`, `network.profile: "public"`,
	}}
	if got.Kind != want.Kind || !slices.Equal(got.Entries, want.Entries) {
		t.Errorf("config input = %+v, want %+v", got, want)
	}

	before := f.compute(t).Digest()
	f.write(t, "config.yaml", "memory: '8G'   # same values\nmounts: {/host: /}\nnetwork: {profile: public}\n")
	if after := f.compute(t).Digest(); after != before {
		t.Error("a reformatted config with the same values changed the digest")
	}
	f.write(t, "config.yaml", "memory: 16G\nmounts: {/host: /}\nnetwork: {profile: public}\n")
	if after := f.compute(t).Digest(); after == before {
		t.Error("a changed config value kept the digest")
	}
}

func TestComputeEnvFileIsItsVariables(t *testing.T) {
	f := newProjectFixture(t)
	path := f.write(t, "env", "# comment\nB=2\nA=1\n")
	got := inputAt(t, f.compute(t), path)
	if got.Kind != KindEnv || !slices.Equal(got.Entries, []string{"A=1", "B=2"}) {
		t.Errorf("env input = %+v, want the sorted variables", got)
	}
}

// TestComputeSecretFilesAreStructureOnly pins that a secret's hosts and scope
// are gated while its value never reaches the projection.
func TestComputeSecretFilesAreStructureOnly(t *testing.T) {
	f := newProjectFixture(t)
	legacy := f.write(t, "env.secret", "GH_TOKEN=ghp_legacyvalue@api.github.com\n")
	yamlFile := f.write(t, "env.secret.yaml",
		"API_KEY:\n  value: sk_yamlvalue\n  hosts: [b.example.com, a.example.com]\n  header_fields: [authorization]\n"+
			"ANY:\n  value: anyvalue\n  allow_any_host_dangerous: true\n"+
			"ONE:\n  value: onevalue\n  host: one.example.com\n"+
			"PLAIN:\n  value: plainvalue\n")
	projection := f.compute(t)

	if got := inputAt(t, projection, legacy); got.Kind != KindSecrets ||
		!slices.Equal(got.Entries, []string{"GH_TOKEN: hosts api.github.com"}) {
		t.Errorf("legacy secret input = %+v", got)
	}
	want := []string{
		"ANY: any host (dangerous)",
		"API_KEY: hosts a.example.com, b.example.com; header fields authorization",
		"ONE: hosts one.example.com",
		"PLAIN",
	}
	if got := inputAt(t, projection, yamlFile); got.Kind != KindSecrets || !slices.Equal(got.Entries, want) {
		t.Errorf("yaml secret input = %+v, want entries %q", got, want)
	}
	for _, input := range projection.Inputs {
		for _, entry := range input.Entries {
			for _, value := range []string{"ghp_legacyvalue", "sk_yamlvalue", "anyvalue", "onevalue", "plainvalue"} {
				if strings.Contains(entry, value) {
					t.Fatalf("entry %q of %s leaks the secret value", entry, input.Path)
				}
			}
		}
	}
}

func TestComputeFileInputsAreTheirContent(t *testing.T) {
	f := newProjectFixture(t)
	dockerfile := f.write(t, "Dockerfile", "FROM base\nRUN make\n")
	snippet := f.write(t, "opencode/opencode.json", `{"model":"x"}`)
	plugin := f.write(t, "opencode2/plugin/a.ts", "export {}\n")
	stray := f.write(t, "README.md", "not consumed\n")
	projection := f.compute(t)

	for _, tc := range []struct {
		path string
		want []string
	}{
		{dockerfile, []string{"FROM base", "RUN make"}},
		{snippet, []string{`{"model":"x"}`}},
		{plugin, []string{"export {}"}},
	} {
		if got := inputAt(t, projection, tc.path); got.Kind != KindFile || !slices.Equal(got.Entries, tc.want) {
			t.Errorf("input %s = %+v, want content %q", tc.path, got, tc.want)
		}
	}
	for _, input := range projection.Inputs {
		if input.Path == stray {
			t.Errorf("a file the launcher does not consume was gated: %s", stray)
		}
	}
}

func TestComputeLargeOrBinaryFilesAreADigest(t *testing.T) {
	f := newProjectFixture(t)
	large := f.write(t, "opencode/big.txt", strings.Repeat("x", maxContentBytes+1))
	binary := f.write(t, "opencode/bin.dat", "\xff\xfe\x00")
	projection := f.compute(t)
	for _, path := range []string{large, binary} {
		got := inputAt(t, projection, path)
		if len(got.Entries) != 1 || !strings.HasPrefix(got.Entries[0], "sha256:") {
			t.Errorf("input %s = %+v, want a single sha256 entry", path, got.Entries)
		}
	}
}

// TestComputeHomeSourcesAreGatedByTheirResolvedPath pins that a home: source
// the guest can write is gated by content however the entry spells it, and a
// host path outside the checkout adds no input beyond the config entry.
func TestComputeHomeSourcesAreGatedByTheirResolvedPath(t *testing.T) {
	f := newProjectFixture(t)
	hook := f.write(t, "github-auth.sh", "#!/bin/sh\necho hi\n")
	checkoutScript := filepath.Join(f.checkout, "scripts", "setup.sh")
	writeFile(t, checkoutScript, "make setup\n")
	checkoutDir := filepath.Join(f.checkout, "dotfiles")
	writeFile(t, filepath.Join(checkoutDir, "a.conf"), "a\n")
	outside := filepath.Join(t.TempDir(), "host.conf")
	writeFile(t, outside, "host\n")
	f.write(t, "config.yaml", "home:\n"+
		"  bin/auth.sh: {source: github-auth.sh, hook: startup}\n"+
		"  setup.sh: "+checkoutScript+"\n"+
		"  .dotfiles: "+checkoutDir+"\n"+
		"  .missing: missing.sh\n"+
		"  .host.conf: "+outside+"\n")
	projection := f.compute(t)

	for path, want := range map[string][]string{
		hook:                                 {"#!/bin/sh", "echo hi"},
		checkoutScript:                       {"make setup"},
		filepath.Join(checkoutDir, "a.conf"): {"a"},
	} {
		if got := inputAt(t, projection, path); got.Kind != KindFile || !slices.Equal(got.Entries, want) {
			t.Errorf("home source %s = %+v, want content %q", path, got, want)
		}
	}
	for _, input := range projection.Inputs {
		if input.Path == outside {
			t.Errorf("a host path outside the checkout was gated by content: %s", outside)
		}
	}
}

// TestComputeHomeSourceThroughASymlinkIntoTheCheckoutIsGated pins that a source
// spelled outside the checkout but resolving into it through a symlink is
// still gated by content, since the guest can write the target.
func TestComputeHomeSourceThroughASymlinkIntoTheCheckoutIsGated(t *testing.T) {
	f := newProjectFixture(t)
	target := filepath.Join(f.checkout, "hook.sh")
	writeFile(t, target, "echo hook\n")
	link := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	f.write(t, "config.yaml", "home:\n  hook.sh: "+link+"\n")
	if got := inputAt(t, f.compute(t), link); !slices.Equal(got.Entries, []string{"echo hook"}) {
		t.Errorf("symlinked home source = %+v, want the target's content", got)
	}
}

func TestComputeDigestTracksPathsAndIgnoresOrder(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "opencode/a.json", "{}")
	before := f.compute(t)
	if err := os.Rename(filepath.Join(f.projectDir, "opencode", "a.json"),
		filepath.Join(f.projectDir, "opencode", "b.json")); err != nil {
		t.Fatal(err)
	}
	if f.compute(t).Digest() == before.Digest() {
		t.Error("moving a file kept the digest")
	}
	reversed := Projection{Inputs: slices.Clone(before.Inputs)}
	slices.Reverse(reversed.Inputs)
	if reversed.Digest() != before.Digest() {
		t.Error("the input order changed the digest")
	}
}

func TestComputeReportsAnUnparsableConfig(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "config.yaml", "memory: [unclosed\n")
	if _, err := Compute(Sources{ProjectConfigDir: f.projectDir, CheckoutRoot: f.checkout}); err == nil {
		t.Error("Compute accepted an unparsable project config")
	}
}

func TestComputeReportsAnInvalidHomeSection(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "config.yaml", "home: [not, a, map]\n")
	if _, err := Compute(Sources{ProjectConfigDir: f.projectDir, CheckoutRoot: f.checkout}); err == nil {
		t.Error("Compute accepted an invalid home: section")
	}
}

// TestDigestTreatsEmptyAndAbsentEntriesAlike pins that a stored approval, which
// drops empty entries, still matches the projection it approved.
func TestDigestTreatsEmptyAndAbsentEntriesAlike(t *testing.T) {
	empty := projectionOf(Input{Path: "/p/env", Kind: KindEnv, Entries: []string{}})
	absent := projectionOf(Input{Path: "/p/env", Kind: KindEnv})
	if empty.Digest() != absent.Digest() {
		t.Error("empty and absent entries hash differently")
	}
}

// TestGuestWritableChecksEachRootAsSpelledAndResolved pins the roots test: an
// unset root never matches, a root that does not exist is compared as spelled.
func TestGuestWritableChecksEachRootAsSpelledAndResolved(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "gone")
	if !guestWritable(filepath.Join(missingRoot, "a.sh"), "", missingRoot) {
		t.Error("a path under a missing root was not matched as spelled")
	}
	if guestWritable("/elsewhere/a.sh", "", missingRoot) {
		t.Error("a path outside every root was matched")
	}
}

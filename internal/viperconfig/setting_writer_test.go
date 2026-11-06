package viperconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

const settingSlug = "widget-bbb"

// TestLayerSettingOwnerPrefersProjectLayer pins the precedence order: when the
// project config sets the key, its directory wins even though the lower user
// layers set it too.
func TestLayerSettingOwnerPrefersProjectLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": "2G"})
	testutil.WriteYAML(t, cp.UserSlugConfigDir(settingSlug), "config.yaml", map[string]any{"memory": "4G"})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{"memory": "8G"})

	dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
	if !ok {
		t.Fatal("LayerSettingOwner reported no owner, want the project layer")
	}
	if dir != cp.ProjectConfigDir() {
		t.Errorf("dir = %q, want the project config dir %q", dir, cp.ProjectConfigDir())
	}
}

// TestLayerSettingOwnerPicksUserSlugLayer pins that a key only the per-slug user
// layer sets resolves to that layer, not the generic user layer that lacks it.
func TestLayerSettingOwnerPicksUserSlugLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	testutil.WriteYAML(t, cp.UserSlugConfigDir(settingSlug), "config.yaml", map[string]any{"memory": "4G"})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{"tmp-size": "1G"})

	dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
	if !ok {
		t.Fatal("LayerSettingOwner reported no owner, want the per-slug user layer")
	}
	if dir != cp.UserSlugConfigDir(settingSlug) {
		t.Errorf("dir = %q, want the per-slug user config dir %q", dir, cp.UserSlugConfigDir(settingSlug))
	}
}

// TestLayerSettingOwnerPicksGenericUserLayer pins that the generic user layer is
// the owner when it is the only one setting the key.
func TestLayerSettingOwnerPicksGenericUserLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": "2G"})
	testutil.WriteYAML(t, cp.UserSlugConfigDir(settingSlug), "config.yaml", map[string]any{"cpus": 2})

	dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
	if !ok {
		t.Fatal("LayerSettingOwner reported no owner, want the generic user layer")
	}
	if dir != cp.UserConfigDir() {
		t.Errorf("dir = %q, want the generic user config dir %q", dir, cp.UserConfigDir())
	}
}

// TestLayerSettingOwnerNone pins that no owner is reported when no file layer
// sets the key.
func TestLayerSettingOwnerNone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{"tmp-size": "1G"})

	dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
	if ok {
		t.Fatalf("LayerSettingOwner = %q, true; want no owner", dir)
	}
}

// TestLayerSettingOwnerEmptySlugSkipsSlugLayer pins that an empty slug does not
// consult the per-slug user layer, matching ConfigLayers.
func TestLayerSettingOwnerEmptySlugSkipsSlugLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})

	dir, ok := LayerSettingOwner(configpaths.Get(), "", "memory")
	if ok {
		t.Fatalf("LayerSettingOwner = %q, true; want no owner", dir)
	}
}

// TestLayerSettingOwnerNestedKey pins that a dotted key resolves through nested
// mappings in a file layer.
func TestLayerSettingOwnerNestedKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.yaml", "network:\n  profile: private\n")

	dir, ok := LayerSettingOwner(configpaths.Get(), "", "network.profile")
	if !ok {
		t.Fatal("LayerSettingOwner reported no owner for a nested key")
	}
	if dir != cp.UserConfigDir() {
		t.Errorf("dir = %q, want %q", dir, cp.UserConfigDir())
	}
}

// TestLayerSettingOwnerMatchesKeyCaseInsensitively pins that the owner scan
// finds a key however it is spelled, at every level of a dotted key: viper
// resolves `memory` from a `Memory:` entry and `network.profile` from a
// `Network:` / `Profile:` pair, so the layer must be reported as the owner or
// an accepted raise would be written to a lower-precedence layer and silently
// lose. The project layer's `Memory:` owns `memory` over the lower per-slug
// user layer's `memory:`, and its differently-cased nested key owns
// `network.profile`.
func TestLayerSettingOwnerMatchesKeyCaseInsensitively(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserSlugConfigDir(settingSlug), "config.yaml", "memory: 4G\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "Network:\n  Profile: private\nMemory: 8G\n")

	dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
	if !ok || dir != cp.ProjectConfigDir() {
		t.Errorf("LayerSettingOwner(memory) = %q, %v; want the project config dir %q", dir, ok, cp.ProjectConfigDir())
	}
	dir, ok = LayerSettingOwner(configpaths.Get(), settingSlug, "network.profile")
	if !ok || dir != cp.ProjectConfigDir() {
		t.Errorf(
			"LayerSettingOwner(network.profile) = %q, %v; want the project config dir %q",
			dir,
			ok,
			cp.ProjectConfigDir(),
		)
	}
}

// TestLayerSettingOwnerReadsEveryFormat pins that the owner scan parses YAML,
// JSON, and JSON5 layers alike.
func TestLayerSettingOwnerReadsEveryFormat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ext     string
		content string
	}{
		{name: "json", ext: ".json", content: `{"memory": "8G"}`},
		{name: "jsonc", ext: ".jsonc", content: "{\n  // a comment\n  \"memory\": \"8G\",\n}\n"},
		{name: "json5", ext: ".json5", content: "{memory: '8G',}"},
		{name: "yml", ext: ".yml", content: "memory: 8G\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			cp := configpaths.Get()
			testutil.WriteFile(t, cp.ProjectConfigDir(), "config"+tc.ext, tc.content)

			dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
			if !ok {
				t.Fatal("LayerSettingOwner reported no owner")
			}
			if dir != cp.ProjectConfigDir() {
				t.Errorf("dir = %q, want %q", dir, cp.ProjectConfigDir())
			}
		})
	}
}

// TestLayerSettingOwnerSkipsUnreadableLayers pins that an unparseable higher
// layer is skipped rather than aborting resolution, so a lower file layer that
// sets the key still wins.
func TestLayerSettingOwnerSkipsUnreadableLayers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ext     string
		content string
	}{
		{name: "yaml", ext: ".yaml", content: "memory: [unclosed\n"},
		{name: "json", ext: ".json", content: `{"memory": `},
		{name: "json5", ext: ".json5", content: "{memory: }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			cp := configpaths.Get()
			testutil.WriteFile(t, cp.ProjectConfigDir(), "config"+tc.ext, tc.content)
			testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": "2G"})

			dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
			if !ok {
				t.Fatal("LayerSettingOwner reported no owner")
			}
			if dir != cp.UserConfigDir() {
				t.Errorf("dir = %q, want the user config dir %q", dir, cp.UserConfigDir())
			}
		})
	}
}

// TestLayerSettingOwnerSkipsUnreadableFile pins that a config path that cannot
// be read (a directory named like the config file) is skipped.
func TestLayerSettingOwnerSkipsUnreadableFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	if err := os.Mkdir(filepath.Join(cp.ProjectConfigDir(), "config.yaml"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": "2G"})

	dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
	if !ok {
		t.Fatal("LayerSettingOwner reported no owner")
	}
	if dir != cp.UserConfigDir() {
		t.Errorf("dir = %q, want the user config dir %q", dir, cp.UserConfigDir())
	}
}

// TestLayerSettingOwnerSkipsPartiallyParsedLayer pins the parse-error guard:
// yaml.v3 populates earlier keys before a later error, so a layer that errors
// after setting the key must still be skipped rather than treating the partial
// map as complete.
func TestLayerSettingOwnerSkipsPartiallyParsedLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "memory: 4G\nx: !!int notanint\n")
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": "2G"})

	dir, ok := LayerSettingOwner(configpaths.Get(), settingSlug, "memory")
	if !ok {
		t.Fatal("LayerSettingOwner reported no owner")
	}
	if dir != cp.UserConfigDir() {
		t.Errorf("dir = %q, want the user config dir %q", dir, cp.UserConfigDir())
	}
}

// TestWriteSettingCreatesConfigFile pins that an empty directory gains a
// config.yaml the resolver reads back.
func TestWriteSettingCreatesConfigFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserSlugConfigDir(settingSlug)

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("config.yaml not created: %v", err)
	}

	r, err := NewResolver(nil, configpaths.Get(), settingSlug)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.Memory != "8G" {
		t.Errorf("Memory = %q, want 8G", r.cfg.Memory)
	}
}

// TestWriteSettingPreservesUnrelatedKeyAndComments pins the format-preserving
// contract: an existing key keeps its value, and both a top-of-file comment and
// a comment attached to another key survive the edit.
func TestWriteSettingPreservesUnrelatedKeyAndComments(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "# top comment\ncpus: 2 # trailing comment\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	got := readTestFile(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(got, "# top comment") {
		t.Errorf("top-of-file comment lost:\n%s", got)
	}
	if !strings.Contains(got, "# trailing comment") {
		t.Errorf("attached comment lost:\n%s", got)
	}
	parsed := parseYAMLMap(t, got)
	if parsed["cpus"] != 2 {
		t.Errorf("cpus = %v, want 2", parsed["cpus"])
	}
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
}

// TestWriteSettingReplacesValueUnderExistingKeySpelling pins the other half of
// the case-insensitivity contract: editing an existing `Memory:` key replaces
// its value in place, keeping the user's spelling, and the resolver then reads
// the replaced value.
func TestWriteSettingReplacesValueUnderExistingKeySpelling(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "Memory: 2G # keep me\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	got := readTestFile(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(got, "Memory:") {
		t.Errorf("key spelling changed; want `Memory:` preserved:\n%s", got)
	}
	if strings.Contains(got, "memory:") {
		t.Errorf("added a second lowercased key instead of replacing `Memory:`:\n%s", got)
	}
	if !strings.Contains(got, "# keep me") {
		t.Errorf("comment on the overwritten key lost:\n%s", got)
	}
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.Memory != "8G" {
		t.Errorf("resolved Memory = %q, want 8G", r.cfg.Memory)
	}
}

// TestWriteSettingPreservesFileBytes pins the format-preserving contract byte
// for byte: only the edited value changes (or one line is appended), so
// comment-only content, blank lines, indentation, quoting elsewhere, and
// trailing comments survive.
func TestWriteSettingPreservesFileBytes(t *testing.T) {
	tests := []struct {
		name     string
		original string
		key      string
		want     string
	}{
		{
			name:     "comment-only file gets the key appended",
			original: "# memory: 8G\n# cpus: 4\n",
			key:      "memory",
			want:     "# memory: 8G\n# cpus: 4\nmemory: 6144M\n",
		},
		{
			name:     "existing plain value keeps indentation and blank lines",
			original: "network:\n  policy: strict\n  allow:\n    - a.com\n\n# blank above\nmemory: 4G\n",
			key:      "memory",
			want:     "network:\n  policy: strict\n  allow:\n    - a.com\n\n# blank above\nmemory: 6144M\n",
		},
		{
			name:     "quoted value keeps its quotes with a trailing comment",
			original: "memory: \"4G\" # sized for builds\ncpus: 2\n",
			key:      "memory",
			want:     "memory: \"6144M\" # sized for builds\ncpus: 2\n",
		},
		{
			name:     "single-quoted value keeps its quotes",
			original: "memory: '4G'\n",
			key:      "memory",
			want:     "memory: '6144M'\n",
		},
		{
			name:     "differently cased key keeps its spelling",
			original: "Memory: 4G\n",
			key:      "memory",
			want:     "Memory: 6144M\n",
		},
		{
			name:     "missing trailing newline before the appended key",
			original: "cpus: 2",
			key:      "memory",
			want:     "cpus: 2\nmemory: 6144M\n",
		},
		{
			name:     "tab before the trailing comment",
			original: "memory: 4G\t# sized for builds\n",
			key:      "memory",
			want:     "memory: 6144M\t# sized for builds\n",
		},
		{
			name:     "trailing whitespace and CRLF line ending",
			original: "memory: 4G  \r\ncpus: 2\r\n",
			key:      "memory",
			want:     "memory: 6144M  \r\ncpus: 2\r\n",
		},
		{
			name:     "escaped double quote keeps double-quote style",
			original: "memory: \"4\\\"G\" # note\ncpus: 2\n",
			key:      "memory",
			want:     "memory: \"6144M\" # note\ncpus: 2\n",
		},
		{
			name:     "escaped single quote keeps single-quote style",
			original: "memory: '4''G' # note\ncpus: 2\n",
			key:      "memory",
			want:     "memory: '6144M' # note\ncpus: 2\n",
		},
		{
			name:     "single-character value before a blank line",
			original: "memory: 4\n\ncpus: 2\n",
			key:      "memory",
			want:     "memory: 6144M\n\ncpus: 2\n",
		},
		{
			name:     "appended after a last line without newline and a blank line",
			original: "a: 1\n\ncpus: 2",
			key:      "memory",
			want:     "a: 1\n\ncpus: 2\nmemory: 6144M\n",
		},
		{
			name:     "empty existing file",
			original: "",
			key:      "memory",
			want:     "memory: 6144M\n",
		},
		{
			name:     "existing nested value",
			original: "network:\n  dns: 1.1.1.1 # resolver\n\ncpus: 2\n",
			key:      "network.dns",
			want:     "network:\n  dns: 6144M # resolver\n\ncpus: 2\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			dir := configpaths.Get().UserConfigDir()
			testutil.WriteFile(t, dir, "config.yaml", tc.original)

			if err := WriteSetting(dir, tc.key, "6144M"); err != nil {
				t.Fatalf("WriteSetting: %v", err)
			}

			if got := readTestFile(t, filepath.Join(dir, "config.yaml")); got != tc.want {
				t.Errorf("config =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestWriteSettingFallsBackToReencoding pins the cases the in-place edit cannot
// express as a single-line text change: they are re-encoded, and the value
// still lands. Escaped quotes in a quoted value are edited in place.
func TestWriteSettingFallsBackToReencoding(t *testing.T) {
	tests := []struct {
		name     string
		original string
	}{
		{name: "flow-style root", original: "{cpus: 2}\n"},
		{name: "literal block value", original: "memory: |\n  4G\ncpus: 2\n"},
		{name: "multi-line plain value", original: "memory: 4\n  G\ncpus: 2\n"},
		{name: "multi-line double-quoted value", original: "memory: \"4\n  G\"\ncpus: 2\n"},
		{name: "multi-line single-quoted value", original: "memory: '4\n  G'\ncpus: 2\n"},
		{name: "appended key would land in a later document", original: "cpus: 2\n---\nother: 1\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			dir := configpaths.Get().UserConfigDir()
			testutil.WriteFile(t, dir, "config.yaml", tc.original)

			if err := WriteSetting(dir, "memory", "6144M"); err != nil {
				t.Fatalf("WriteSetting: %v", err)
			}

			parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
			if parsed["memory"] != "6144M" || parsed["cpus"] != 2 {
				t.Errorf("config = %v, want memory 6144M and cpus 2 kept", parsed)
			}
		})
	}
}

// TestWriteSettingKeepsTheFileMode pins that an edit keeps the config file's
// permissions, so a file other readers rely on does not turn private, while a
// newly created config file stays private.
func TestWriteSettingKeepsTheFileMode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: 4G\n")
	path := filepath.Join(dir, "config.yaml")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	if err := WriteSetting(dir, "memory", "6144M"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("edited file mode = %v (err %v), want 0644", info.Mode().Perm(), err)
	}

	freshDir := configpaths.Get().UserSlugConfigDir(settingSlug)
	if err := WriteSetting(freshDir, "memory", "6144M"); err != nil {
		t.Fatalf("WriteSetting new file: %v", err)
	}
	if info, err := os.Stat(filepath.Join(freshDir, "config.yaml")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("new file mode = %v (err %v), want 0600", info.Mode().Perm(), err)
	}
}

// TestWriteSettingKeepsCommentsOnAReencodedValue pins that a value the in-place
// edit cannot replace (a block scalar) keeps the comments attached to it when
// the document is re-encoded.
func TestWriteSettingKeepsCommentsOnAReencodedValue(t *testing.T) {
	tests := []struct {
		name     string
		original string
		comment  string
	}{
		{name: "line comment", original: "memory: | # line note\n  2G\ncpus: 2\n", comment: "# line note"},
		{name: "head comment", original: "memory:\n  # head note\n  |\n  2G\ncpus: 2\n", comment: "# head note"},
		{name: "foot comment", original: "memory: |\n  2G\n# foot note\n\ncpus: 2\n", comment: "# foot note"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			dir := configpaths.Get().UserConfigDir()
			testutil.WriteFile(t, dir, "config.yaml", tc.original)

			if err := WriteSetting(dir, "memory", "6144M"); err != nil {
				t.Fatalf("WriteSetting: %v", err)
			}

			got := readTestFile(t, filepath.Join(dir, "config.yaml"))
			if !strings.Contains(got, tc.comment) || parseYAMLMap(t, got)["memory"] != "6144M" {
				t.Errorf("config = %q, want memory 6144M and %q kept", got, tc.comment)
			}
		})
	}
}

// TestWriteSettingKeepsCommentWhenValueContainsQuoteHash pins that a value with
// a "#" inside quotes is not confused with a trailing comment: the edited entry
// keeps its trailing comment and the value is replaced (keeping its quote
// style). The value is not the first key, so the edit must pick the right
// entry's comments.
func TestWriteSettingKeepsCommentWhenValueContainsQuoteHash(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "cpus: 2\nmemory: \"4 #G\" # keep me\n")

	if err := WriteSetting(dir, "memory", "6144M"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	got := readTestFile(t, filepath.Join(dir, "config.yaml"))
	if got != "cpus: 2\nmemory: \"6144M\" # keep me\n" {
		t.Errorf("config = %q, want the value replaced and only the comment kept", got)
	}
}

// TestWriteSettingReencodesAMultiLineValue pins that a value whose rendering
// spans several lines is not spliced into one line: the document is re-encoded
// with two-space indentation.
func TestWriteSettingReencodesAMultiLineValue(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "cpus: 2\n")

	if err := WriteSetting(dir, "note", "first\nsecond"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	const want = "cpus: 2\nnote: |-\n  first\n  second\n"
	if got := readTestFile(t, filepath.Join(dir, "config.yaml")); got != want {
		t.Errorf("config =\n%q\nwant\n%q", got, want)
	}
}

// TestWriteSettingNestedKeyKeepsTwoSpaceIndent pins that an edit the in-place
// path cannot express (a new nested key) re-encodes with two-space indentation,
// matching how config files are written by hand.
func TestWriteSettingNestedKeyKeepsTwoSpaceIndent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "network:\n  profile: private\n")

	if err := WriteSetting(dir, "network.dns", "1.1.1.1"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	const want = "network:\n  profile: private\n  dns: 1.1.1.1\n"
	if got := readTestFile(t, filepath.Join(dir, "config.yaml")); got != want {
		t.Errorf("config =\n%q\nwant\n%q", got, want)
	}
}

// TestWriteSettingOverwritesExistingKey pins that setting an already-present key
// replaces its value in place.
func TestWriteSettingOverwritesExistingKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: 2G\ncpus: 2\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
	if parsed["cpus"] != 2 {
		t.Errorf("cpus = %v, want 2", parsed["cpus"])
	}
}

// TestWriteSettingOverwritesDifferentlyCasedKey pins that the writer replaces
// an existing key spelled with different case instead of adding a second entry
// that the resolver would see as the same key.
func TestWriteSettingOverwritesDifferentlyCasedKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "Memory: 2G\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	if len(parsed) != 1 || parsed["Memory"] != "8G" {
		t.Errorf("config = %v, want the single existing Memory key set to 8G", parsed)
	}
}

// TestWriteSettingPreservesCommentOnOverwrittenKey pins that overwriting a key's
// value does not drop the comments attached to that key.
func TestWriteSettingPreservesCommentOnOverwrittenKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: 2G # keep me\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	got := readTestFile(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(got, "# keep me") {
		t.Errorf("comment on the overwritten key lost:\n%s", got)
	}
	if parsed := parseYAMLMap(t, got); parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
}

// TestWriteSettingEditsSymlinkTarget pins that a symlinked config file keeps its
// link: the atomic write edits the target, not the link itself.
func TestWriteSettingEditsSymlinkTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(t.TempDir(), "dotfiles-config.yaml")
	testutil.WriteFile(t, filepath.Dir(target), filepath.Base(target), "memory: 2G\n")
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("config.yaml is no longer a symlink")
	}
	parsed := parseYAMLMap(t, readTestFile(t, target))
	if parsed["memory"] != "8G" {
		t.Errorf("target memory = %v, want 8G", parsed["memory"])
	}
}

// TestWriteSettingSymlinkResolutionError pins that a link-resolution failure is
// surfaced instead of editing the link path.
func TestWriteSettingSymlinkResolutionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: 2G\n")
	orig := evalSymlinks
	evalSymlinks = func(string) (string, error) { return "", errors.New("link loop") }
	t.Cleanup(func() { evalSymlinks = orig })

	err := WriteSetting(dir, "memory", "8G")
	if err == nil || !strings.Contains(err.Error(), "resolve config file link") {
		t.Fatalf("WriteSetting error = %v, want the link-resolution failure", err)
	}
	requireWrapped(t, err)
}

// TestWriteSettingRefusesAnchoredValueReferencedElsewhere pins that an anchored
// value another key aliases is left untouched: replacing it would remove the
// anchor and leave the alias dangling.
func TestWriteSettingRefusesAnchoredValueReferencedElsewhere(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	const original = "memory: &size 4G\ntmp-size: *size\n"
	testutil.WriteFile(t, dir, "config.yaml", original)

	err := WriteSetting(dir, "memory", "8G")
	if err == nil {
		t.Fatal("expected WriteSetting to refuse a referenced anchor")
	}
	requireWrapped(t, err)
	if got := readTestFile(t, filepath.Join(dir, "config.yaml")); got != original {
		t.Errorf("config changed despite refusal:\n%s", got)
	}
}

// TestWriteSettingEditsUnreferencedAnchor pins that an anchor with no aliases is
// replaceable: removing it leaves no dangling reference.
func TestWriteSettingEditsUnreferencedAnchor(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: &size 4G\ncpus: 2\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
	if parsed["cpus"] != 2 {
		t.Errorf("cpus = %v, want 2", parsed["cpus"])
	}
}

// TestWriteSettingEmptyFile pins that an existing but empty config file is
// treated as an empty mapping.
func TestWriteSettingEmptyFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
}

// TestWriteSettingKeepsYAMLExtension pins that a .yml file stays YAML and keeps
// its extension.
func TestWriteSettingKeepsYAMLExtension(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yml", "memory: 2G\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yml")))
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
}

// TestWriteSettingNestedKey pins that a dotted key creates the nested mapping.
func TestWriteSettingNestedKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "network:\n  profile: private\n")

	if err := WriteSetting(dir, "network.dns-servers", []any{"1.1.1.1"}); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	network, ok := parsed["network"].(map[string]any)
	if !ok {
		t.Fatalf("network is %T, want a mapping", parsed["network"])
	}
	if network["profile"] != "private" {
		t.Errorf("network.profile = %v, want private", network["profile"])
	}
	if servers, ok := network["dns-servers"].([]any); !ok || len(servers) != 1 || servers[0] != "1.1.1.1" {
		t.Errorf("network.dns-servers = %v, want [1.1.1.1]", network["dns-servers"])
	}
}

// TestWriteSettingOverwritesSecondKey pins the mapping pair-index arithmetic:
// editing a key that is not the first entry must replace exactly its value and
// keep its neighbours, so an index that drifts off the pair corrupts the file.
func TestWriteSettingOverwritesSecondKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "cpus: 2\nmemory: 4G # keep me\ntmp-size: 1G\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	got := readTestFile(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(got, "# keep me") {
		t.Errorf("comment on the second key lost:\n%s", got)
	}
	parsed := parseYAMLMap(t, got)
	if parsed["cpus"] != 2 {
		t.Errorf("cpus = %v, want 2", parsed["cpus"])
	}
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
	if parsed["tmp-size"] != "1G" {
		t.Errorf("tmp-size = %v, want 1G", parsed["tmp-size"])
	}
}

// TestWriteSettingNestedKeyAfterAnotherKey pins descending into a mapping that
// is not the first entry of the document.
func TestWriteSettingNestedKeyAfterAnotherKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "cpus: 2\nnetwork:\n  profile: private\n")

	if err := WriteSetting(dir, "network.dns-servers", []any{"1.1.1.1"}); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	network, ok := parsed["network"].(map[string]any)
	if !ok {
		t.Fatalf("network is %T, want a mapping", parsed["network"])
	}
	if network["profile"] != "private" {
		t.Errorf("network.profile = %v, want private", network["profile"])
	}
	if servers, ok := network["dns-servers"].([]any); !ok || len(servers) != 1 || servers[0] != "1.1.1.1" {
		t.Errorf("network.dns-servers = %v, want [1.1.1.1]", network["dns-servers"])
	}
	if parsed["cpus"] != 2 {
		t.Errorf("cpus = %v, want 2", parsed["cpus"])
	}
}

// TestWriteSettingEditsAnchorWithUnrelatedAlias pins that an alias to a
// different anchor does not block replacing the anchor's value.
func TestWriteSettingEditsAnchorWithUnrelatedAlias(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: &size 4G\ntmp-size: &other 2G\nquota: *other\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}
	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
}

// TestWriteSettingPreservesHeadCommentOnOverwrittenKey pins that a block comment
// above the key is carried onto the replacement value node.
func TestWriteSettingPreservesHeadCommentOnOverwrittenKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "# head comment\nmemory: 2G\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}
	got := readTestFile(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(got, "# head comment") {
		t.Errorf("head comment lost:\n%s", got)
	}
}

// TestWriteSettingEditsAnchorWithScalarMatchingAnchorName pins that a plain
// scalar whose text happens to equal an anchor name does not count as a
// reference to that anchor.
func TestWriteSettingEditsAnchorWithScalarMatchingAnchorName(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: &size 4G\ncpus: size\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}
	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
}

// TestWriteSettingRefusesNestedAnchoredValueReferencedElsewhere pins the nested
// anchor detection: the value is found at its nested path and refused when an
// alias references it.
func TestWriteSettingRefusesNestedAnchoredValueReferencedElsewhere(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	const original = "cpus: 2\nnet:\n  profile: &p private\nother: *p\n"
	testutil.WriteFile(t, dir, "config.yaml", original)

	err := WriteSetting(dir, "net.profile", "public")
	if err == nil {
		t.Fatal("expected WriteSetting to refuse a nested referenced anchor")
	}
	if got := readTestFile(t, filepath.Join(dir, "config.yaml")); got != original {
		t.Errorf("config changed despite refusal:\n%s", got)
	}
}

// TestWriteSettingOverwritesKeyWhoseNameAppearsAsAValue pins that the key scan
// only inspects key nodes: a value string equal to the target key name must not
// be mistaken for the key.
func TestWriteSettingOverwritesKeyWhoseNameAppearsAsAValue(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "note: memory\nmemory: 4G\n")

	if err := WriteSetting(dir, "memory", "8G"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}
	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	if parsed["memory"] != "8G" {
		t.Errorf("memory = %v, want 8G", parsed["memory"])
	}
	if parsed["note"] != "memory" {
		t.Errorf("note = %v, want memory (unchanged)", parsed["note"])
	}
}

// TestWriteSettingSingleCharacterRootRefused pins that a one-character
// non-mapping root is detected rather than treated as an empty document.
func TestWriteSettingSingleCharacterRootRefused(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	path := filepath.Join(dir, "config.yaml")
	testutil.WriteFile(t, dir, "config.yaml", "x")

	err := WriteSetting(dir, "memory", "8G")
	if err == nil {
		t.Fatal("expected an error for a one-character non-mapping root")
	}
	if got := readTestFile(t, path); got != "x" {
		t.Errorf("file changed: %q", got)
	}
}

// TestWriteSettingRejectsNonYAML pins that a JSON5/JSON/JSONC config file is
// never rewritten and is left byte-identical.
func TestWriteSettingRejectsNonYAML(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ext     string
		content string
	}{
		{name: "json5", ext: ".json5", content: "{\n  // comment\n  \"memory\": \"2G\",\n}\n"},
		{name: "jsonc", ext: ".jsonc", content: "{\n  /* comment */\n  \"memory\": \"2G\"\n}\n"},
		{name: "json", ext: ".json", content: "{\"memory\": \"2G\"}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			dir := configpaths.Get().UserConfigDir()
			path := filepath.Join(dir, "config"+tc.ext)
			testutil.WriteFile(t, dir, "config"+tc.ext, tc.content)

			err := WriteSetting(dir, "memory", "8G")
			if !errors.Is(err, ErrUnsupportedConfigFormat) {
				t.Fatalf("err = %v, want ErrUnsupportedConfigFormat", err)
			}
			if got := readTestFile(t, path); got != tc.content {
				t.Errorf("file changed:\n got %q\nwant %q", got, tc.content)
			}
		})
	}
}

// TestWriteSettingInvalidYAML pins that an unparseable YAML file is surfaced as
// an error and left untouched.
func TestWriteSettingInvalidYAML(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	path := filepath.Join(dir, "config.yaml")
	testutil.WriteFile(t, dir, "config.yaml", "memory: [unclosed\n")

	err := WriteSetting(dir, "memory", "8G")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "invalid YAML") {
		t.Errorf("err = %v, want it to mention invalid YAML", err)
	}
	if got := readTestFile(t, path); got != "memory: [unclosed\n" {
		t.Errorf("file changed: %q", got)
	}
}

// TestWriteSettingUnreadableFile pins that an existing config path that cannot
// be read (a directory) is surfaced as an error.
func TestWriteSettingUnreadableFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	if err := os.Mkdir(filepath.Join(dir, "config.yaml"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := WriteSetting(dir, "memory", "8G"); err == nil {
		t.Fatal("expected a read error")
	} else {
		requireWrapped(t, err)
		if !strings.Contains(err.Error(), "read config file") {
			t.Errorf("error = %v, want the read failure reported", err)
		}
	}
}

// TestWriteSettingNonMappingRoot pins that a config file whose root is not a
// mapping is refused rather than rewritten.
func TestWriteSettingNonMappingRoot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	path := filepath.Join(dir, "config.yaml")
	testutil.WriteFile(t, dir, "config.yaml", "- one\n- two\n")

	err := WriteSetting(dir, "memory", "8G")
	if err == nil {
		t.Fatal("expected an error for a non-mapping root")
	}
	requireWrapped(t, err)
	if got := readTestFile(t, path); got != "- one\n- two\n" {
		t.Errorf("file changed: %q", got)
	}
}

// TestWriteSettingIntermediateNotMapping pins that a dotted key cannot descend
// through a scalar.
func TestWriteSettingIntermediateNotMapping(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: 2G\n")

	if err := WriteSetting(dir, "memory.foo", "bar"); err == nil {
		t.Fatal("expected an error descending through a scalar")
	} else {
		requireWrapped(t, err)
	}
}

// failingTextMarshaler is a value the YAML encoder rejects through the
// TextMarshaler error path.
type failingTextMarshaler struct{}

func (failingTextMarshaler) MarshalText() ([]byte, error) {
	return nil, errors.New("boom")
}

// TestWriteSettingUnencodableValue pins that a value the YAML encoder rejects is
// surfaced and nothing is written.
func TestWriteSettingUnencodableValue(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserSlugConfigDir(settingSlug)

	if err := WriteSetting(dir, "memory", failingTextMarshaler{}); err == nil {
		t.Fatal("expected an encode error")
	} else {
		requireWrapped(t, err)
		requireWrapped(t, errors.Unwrap(err))
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("config.yaml should not exist after a failed write, stat err = %v", err)
	}
}

// TestLayerSettingOwnerNestedKeyThroughScalar pins that a dotted key whose
// intermediate value is a scalar does not resolve.
func TestLayerSettingOwnerNestedKeyThroughScalar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.yaml", "network: private\n")

	if _, ok := LayerSettingOwner(configpaths.Get(), "", "network.profile"); ok {
		t.Fatal("LayerSettingOwner resolved a key through a scalar")
	}
}

// TestWriteSettingCreatesIntermediateMapping pins that a dotted key whose
// intermediate mapping is absent creates it.
func TestWriteSettingCreatesIntermediateMapping(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()

	if err := WriteSetting(dir, "a.b", "c"); err != nil {
		t.Fatalf("WriteSetting: %v", err)
	}

	parsed := parseYAMLMap(t, readTestFile(t, filepath.Join(dir, "config.yaml")))
	nested, ok := parsed["a"].(map[string]any)
	if !ok {
		t.Fatalf("a is %T, want a mapping", parsed["a"])
	}
	if nested["b"] != "c" {
		t.Errorf("a.b = %v, want c", nested["b"])
	}
}

// TestWriteSettingRefusesOversizedInput pins that the edit path parses under the
// untrusted limits: a scalar beyond the scalar cap is refused and the file is
// left untouched, so a hostile project config cannot drive unbounded memory.
func TestWriteSettingRefusesOversizedInput(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserConfigDir()
	path := filepath.Join(dir, "config.yaml")
	oversized := "note: " + strings.Repeat("x", 17<<20) + "\n" // above the 16 MiB untrusted scalar cap
	testutil.WriteFile(t, dir, "config.yaml", oversized)

	if err := WriteSetting(dir, "memory", "8G"); err == nil {
		t.Fatal("expected the oversized scalar to be refused")
	}
	if got := readTestFile(t, path); got != oversized {
		t.Errorf("file changed despite the refusal")
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// requireWrapped fails when err does not wrap an underlying cause, pinning a
// fmt.Errorf %w (which yields an unwrappable error) over a %v (which does not).
func requireWrapped(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("requireWrapped: nil error")
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %v is not a %%w wrap (no unwrappable cause)", err)
	}
}

func parseYAMLMap(t *testing.T, data string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("parse yaml %q: %v", data, err)
	}
	return parsed
}

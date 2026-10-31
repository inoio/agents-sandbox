package reprovision

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

var opencodeLikeRule = agent.ProvisionRule{
	Dir:      ".config/opencode",
	Patterns: []string{"**", "!node_modules/", "!package*.json", "!.gitignore"},
}

func writeHostManifest(t *testing.T, hostHome, content string) string {
	t.Helper()
	dir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	path := filepath.Join(dir, "package.json")
	testutil.WritePath(t, path, content)
	return path
}

func TestUnprovisionedDependencyManifestsReportsDeclaredDependencies(t *testing.T) {
	hostHome := t.TempDir()
	manifest := writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)

	got := unprovisionedDependencyManifests(
		[]agent.ProvisionRule{opencodeLikeRule}, hostHome, nil, nil,
	)

	if !slices.Equal(got, []string{manifest}) {
		t.Errorf("manifests = %v, want [%s]", got, manifest)
	}
}

func TestUnprovisionedDependencyManifestsIgnoresManifestsWithoutDependencies(t *testing.T) {
	cases := map[string]string{
		"empty dependencies":     `{"dependencies": {}}`,
		"no dependencies key":    `{"name": "config"}`,
		"only devDependencies":   `{"devDependencies": {"typescript": "5.0.0"}}`,
		"malformed json":         `{"dependencies": `,
		"dependencies not a map": `{"dependencies": "zod"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			hostHome := t.TempDir()
			writeHostManifest(t, hostHome, content)

			got := unprovisionedDependencyManifests(
				[]agent.ProvisionRule{opencodeLikeRule}, hostHome, nil, nil,
			)

			if len(got) != 0 {
				t.Errorf("manifests = %v, want none", got)
			}
		})
	}
}

func TestUnprovisionedDependencyManifestsIgnoresMissingManifest(t *testing.T) {
	got := unprovisionedDependencyManifests(
		[]agent.ProvisionRule{opencodeLikeRule}, t.TempDir(), nil, nil,
	)

	if len(got) != 0 {
		t.Errorf("manifests = %v, want none", got)
	}
}

func TestUnprovisionedDependencyManifestsIgnoresRulesThatCopyNodeModules(t *testing.T) {
	hostHome := t.TempDir()
	writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)
	copiesEverything := agent.ProvisionRule{Dir: ".config/opencode", Patterns: []string{"**"}}

	got := unprovisionedDependencyManifests(
		[]agent.ProvisionRule{copiesEverything}, hostHome, nil, nil,
	)

	if len(got) != 0 {
		t.Errorf("manifests = %v, want none", got)
	}
}

func TestUnprovisionedDependencyManifestsSkipsDirlessRules(t *testing.T) {
	hostHome := t.TempDir()
	testutil.WritePath(t, filepath.Join(hostHome, "package.json"), `{"dependencies": {"zod": "4.1.8"}}`)
	dirless := agent.ProvisionRule{Dir: "", Patterns: []string{"**", "!node_modules/"}}

	got := unprovisionedDependencyManifests([]agent.ProvisionRule{dirless}, hostHome, nil, nil)

	if len(got) != 0 {
		t.Errorf("manifests = %v, want none", got)
	}
}

func TestUnprovisionedDependencyManifestsSuppressedBySuppliedVMFiles(t *testing.T) {
	cases := map[string]string{
		"manifest supplied":     "/home/dev/.config/opencode/package.json",
		"node_modules supplied": "/home/dev/.config/opencode/node_modules/zod/package.json",
		"ancestor node_modules": "/home/dev/node_modules/zod/package.json",
	}
	for name, suppliedPath := range cases {
		t.Run(name, func(t *testing.T) {
			hostHome := t.TempDir()
			writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)

			got := unprovisionedDependencyManifests(
				[]agent.ProvisionRule{opencodeLikeRule}, hostHome, []string{suppliedPath}, nil,
			)

			if len(got) != 0 {
				t.Errorf("manifests = %v, want none", got)
			}
		})
	}
}

func TestUnprovisionedDependencyManifestsNotSuppressedByUnrelatedVMFiles(t *testing.T) {
	hostHome := t.TempDir()
	manifest := writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)
	unrelated := []string{
		"/home/dev/.config/opencode/plugin/repro.ts",
		"/home/dev/.config/opencode/node_modules_backup/x.js",
		"/home/dev/.config/opencode/package-lock.json",
	}

	got := unprovisionedDependencyManifests(
		[]agent.ProvisionRule{opencodeLikeRule}, hostHome, unrelated, nil,
	)

	if !slices.Equal(got, []string{manifest}) {
		t.Errorf("manifests = %v, want [%s]", got, manifest)
	}
}

func TestUnprovisionedDependencyManifestsSuppressedByMountTargets(t *testing.T) {
	cases := []string{
		"/home/dev/.config/opencode/node_modules",
		"/home/dev/.config/node_modules",
		"/home/dev/node_modules",
		"/node_modules",
		"/home/dev/.config/opencode",
		"/home/dev/.config",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			hostHome := t.TempDir()
			writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)

			got := unprovisionedDependencyManifests(
				[]agent.ProvisionRule{opencodeLikeRule}, hostHome, nil, []string{target},
			)

			if len(got) != 0 {
				t.Errorf("manifests = %v, want none", got)
			}
		})
	}
}

func TestUnprovisionedDependencyManifestsNotSuppressedByUnrelatedMountTargets(t *testing.T) {
	cases := []string{
		"/home/dev/.m2",
		"/home/dev/.config/opencode/plugin/node_modules",
		"/home/dev/.config/opencode/node_modules_backup",
		"/opt/node_modules",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			hostHome := t.TempDir()
			manifest := writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)

			got := unprovisionedDependencyManifests(
				[]agent.ProvisionRule{opencodeLikeRule}, hostHome, nil, []string{target},
			)

			if !slices.Equal(got, []string{manifest}) {
				t.Errorf("manifests = %v, want [%s]", got, manifest)
			}
		})
	}
}

func TestWarnUnprovisionedDependenciesWarnsForTheOpencodeConfigDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome, _ := configpaths.Get().UserHomeDir()
	manifest := writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)
	opencode, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)

	WarnUnprovisionedDependencies(opencode, &ConfigFiles{}, nil, true, &ui)

	if len(ui.WarnCalls) != 1 {
		t.Fatalf("warnings = %v, want exactly one", ui.WarnCalls)
	}
	warning := ui.WarnCalls[0]
	for _, want := range []string{manifest, "node_modules", "package.json", "continuing", "recipes/opencode-plugin-dependencies.html"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning %q does not mention %q", warning, want)
		}
	}
}

func TestWarnUnprovisionedDependenciesSilentWhenHostProvisioningIsDisabled(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome, _ := configpaths.Get().UserHomeDir()
	writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)
	opencode, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)

	WarnUnprovisionedDependencies(opencode, &ConfigFiles{}, nil, false, &ui)

	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none", ui.WarnCalls)
	}
}

func TestWarnUnprovisionedDependenciesSilentWhenHomeOrMirrorSuppliesTheManifest(t *testing.T) {
	suppliedManifest := map[string][]byte{"/home/dev/.config/opencode/package.json": []byte("{}")}
	cases := map[string]*ConfigFiles{
		"home":   {HomeFiles: suppliedManifest},
		"mirror": {Mirror: suppliedManifest},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			hostHome, _ := configpaths.Get().UserHomeDir()
			writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)
			opencode, _ := agent.Lookup("opencode")
			ui := termio.NewTestMock(t)

			WarnUnprovisionedDependencies(opencode, files, nil, true, &ui)

			if len(ui.WarnCalls) != 0 {
				t.Errorf("warnings = %v, want none", ui.WarnCalls)
			}
		})
	}
}

func TestWarnUnprovisionedDependenciesSilentForAgentsNotProvisioningTheOpencodeDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome, _ := configpaths.Get().UserHomeDir()
	writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)
	claude, _ := agent.Lookup("claude-code")
	ui := termio.NewTestMock(t)

	WarnUnprovisionedDependencies(claude, &ConfigFiles{}, nil, true, &ui)

	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none", ui.WarnCalls)
	}
}

func TestWarnUnprovisionedDependenciesSilentWhenHostHomeIsUnknown(t *testing.T) {
	configpaths.WithMockConfigPathsHomeError(t, errors.New("no home"))
	opencode, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)

	WarnUnprovisionedDependencies(opencode, &ConfigFiles{}, nil, true, &ui)

	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none", ui.WarnCalls)
	}
}

func TestWarnUnprovisionedDependenciesSilentForAgentsWithoutProvisionRules(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome, _ := configpaths.Get().UserHomeDir()
	writeHostManifest(t, hostHome, `{"dependencies": {"zod": "4.1.8"}}`)
	ui := termio.NewTestMock(t)

	WarnUnprovisionedDependencies(plainAgent{}, &ConfigFiles{}, nil, true, &ui)

	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none", ui.WarnCalls)
	}
}

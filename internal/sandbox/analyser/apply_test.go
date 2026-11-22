package analyser

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/projecttrust"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

const (
	testSlug         = "widget-abc"
	testCheckoutSlug = "checkout-abc"
)

// interactiveUI returns a UI that reports an interactive terminal. The returned
// counter counts the prompts it answered, so a test can assert the applier
// never asked.
func interactiveUI() (*termiotest.Mock, *int) {
	ui := &termiotest.Mock{IsInteractiveResult: true}
	answered := 0
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		answered++
		return applyFindingKey, nil
	}
	return ui, &answered
}

// decliningUI returns an interactive UI that declines every finding by taking
// the default of Select (the safe answer).
func decliningUI() *termiotest.Mock {
	return &termiotest.Mock{IsInteractiveResult: true}
}

// perSlugWrite builds one per-slug config-key finding.
func perSlugWrite(key string, value any) Finding {
	return Finding{
		Evidence: Evidence{Summary: "the session used " + key},
		Writes:   []Write{{Scope: ScopePerSlugUser, ConfigKey: key, ConfigValue: value}},
	}
}

// unsetKeyEnvironment removes every environment variable bound to key for the
// test's duration, so a developer's or CI's environment cannot shadow the write
// a test expects to succeed.
func unsetKeyEnvironment(t *testing.T, key string) {
	t.Helper()
	for _, name := range viperconfig.EnvVarNames(key) {
		testutil.UnsetEnv(t, name)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists; want nothing written", path)
	}
}

// testManagedConfigAgent is the agent whose config-dir name a managed-config
// write targets: opencode2's primary config-dir name is "opencode2".
const testManagedConfigAgent = "opencode2"

// managedConfigFinding builds one per-slug managed-config finding.
func managedConfigFinding(name, content string) Finding {
	return Finding{
		Evidence: Evidence{Summary: "the session authored " + name},
		Writes: []Write{{
			Scope:         ScopePerSlugUser,
			ManagedConfig: &ManagedConfigWrite{Name: name, Content: []byte(content)},
		}},
	}
}

// TestApplyManagedConfigWritesTheFile pins the managed-config write: the
// finding's content lands at <UserSlugConfigDir>/<agent dir name>/<Name>, the
// managed snippet path the next run's provisioning merges.
func TestApplyManagedConfigWritesTheFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, answered := interactiveUI()

	const content = "{\n  // the session's config\n  \"model\": \"sonnet\"\n}\n"
	results := Apply(
		[]Finding{managedConfigFinding("opencode.jsonc", content)},
		ApplyOptions{
			Paths: cp, Slug: testSlug, Agent: testManagedConfigAgent, UI: ui, Mode: options.ModeRun,
		},
	)

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if *answered != 1 {
		t.Errorf("the write asked %d prompts; want one", *answered)
	}
	against := filepath.Join(cp.UserSlugConfigDir(testSlug), "opencode2")
	if dirs := results[0].WroteDirs; len(dirs) != 1 || dirs[0] != against {
		t.Errorf("WroteDirs = %v; want the agent config dir %q", dirs, against)
	}
	if got := readFile(t, filepath.Join(against, "opencode.jsonc")); got != content {
		t.Errorf("opencode.jsonc = %q; want the finding's content %q", got, content)
	}
	// The managed file is configuration, not a credential (its values are
	// placeholders), so it is created world-readable at 0o644.
	info, err := os.Stat(filepath.Join(against, "opencode.jsonc"))
	if err != nil {
		t.Fatalf("stat opencode.jsonc: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("opencode.jsonc mode = %#o; want 0o644", got)
	}
}

// TestApplyManagedConfigWritesEveryWrite pins that every managed-config write of
// a finding is written: the plan loop must continue past a managed-config write
// rather than stop at it, so a finding carrying more than one managed file lands
// all of them.
func TestApplyManagedConfigWritesEveryWrite(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, _ := interactiveUI()

	against := filepath.Join(cp.UserSlugConfigDir(testSlug), "opencode2")
	finding := Finding{
		Evidence: Evidence{Summary: "the session authored two config files"},
		Writes: []Write{
			{
				Scope:         ScopePerSlugUser,
				ManagedConfig: &ManagedConfigWrite{Name: "opencode.jsonc", Content: []byte("model: sonnet\n")},
			},
			{
				Scope:         ScopePerSlugUser,
				ManagedConfig: &ManagedConfigWrite{Name: "opencode.extra.jsonc", Content: []byte("theme: dark\n")},
			},
		},
	}

	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, Agent: testManagedConfigAgent, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if got := readFile(t, filepath.Join(against, "opencode.jsonc")); got != "model: sonnet\n" {
		t.Errorf("opencode.jsonc = %q; want the first write's content", got)
	}
	if got := readFile(t, filepath.Join(against, "opencode.extra.jsonc")); got != "theme: dark\n" {
		t.Errorf("opencode.extra.jsonc = %q; want the second write's content", got)
	}
}

// TestApplyManagedConfigCreatesTheDir pins that the managed-config write creates
// the agent config directory it targets, so a session's first changed config
// file has somewhere to land.
func TestApplyManagedConfigCreatesTheDir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, _ := interactiveUI()

	against := filepath.Join(cp.UserSlugConfigDir(testSlug), "opencode2")
	if _, err := os.Stat(against); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists before the write; want the agent config dir absent", against)
	}
	const content = "model: sonnet\n"

	results := Apply(
		[]Finding{managedConfigFinding("opencode.jsonc", content)},
		ApplyOptions{
			Paths: cp, Slug: testSlug, Agent: testManagedConfigAgent, UI: ui, Mode: options.ModeRun,
		},
	)

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	info, err := os.Stat(against)
	if err != nil {
		t.Fatalf("stat %s: %v; want the agent config dir created", against, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory; want one", against)
	}
	if got := readFile(t, filepath.Join(against, "opencode.jsonc")); got != content {
		t.Errorf("opencode.jsonc = %q; want the finding's content %q", got, content)
	}
}

// TestApplyManagedConfigProjectScopeIsGated pins that a project-scope
// managed-config write is skipped, not written: the applier has no per-file
// project-trust approval (only ApproveSettingWrite, keyed by a launcher config
// key), so the finding is refused before any prompt or write.
func TestApplyManagedConfigProjectScopeIsGated(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, answered := interactiveUI()

	finding := managedConfigFinding("opencode.jsonc", "model: sonnet\n")
	finding.Writes[0].Scope = ScopeProject

	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug,
		Agent: testManagedConfigAgent, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
		t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
	}
	if *answered != 0 {
		t.Errorf("a project-scope managed write asked %d prompts; want none", *answered)
	}
	assertNoFile(t, filepath.Join(cp.ProjectConfigDir(), "opencode2", "opencode.jsonc"))
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "opencode2", "opencode.jsonc"))
}

// TestApplyManagedConfigRefusesANonPathElementName pins the single-path-element
// guard: the applier joins ManagedConfigWrite.Name under the scope directory, so
// a name that is empty, a dot-segment, or carries a separator is refused before
// any write — nothing may escape the target directory or name a directory.
func TestApplyManagedConfigRefusesANonPathElementName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape.jsonc", "nested/escape.jsonc"} {
		t.Run("name "+strconv.Quote(name), func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			cp := configpaths.Get()
			ui, answered := interactiveUI()

			results := Apply(
				[]Finding{managedConfigFinding(name, "model: sonnet\n")},
				ApplyOptions{Paths: cp, Slug: testSlug, Agent: testManagedConfigAgent, UI: ui, Mode: options.ModeRun},
			)

			if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
				t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
			}
			if *answered != 0 {
				t.Errorf("an invalid managed name asked %d prompts; want none", *answered)
			}
			if strings.ContainsRune(name, filepath.Separator) {
				assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "opencode2", name))
			}
		})
	}
}

// TestApplyManagedConfigRefusesAnEmptyAgent pins that an unset Agent is refused
// rather than silently resolving to the default agent's config dir: the write
// must name the agent it targets.
func TestApplyManagedConfigRefusesAnEmptyAgent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, answered := interactiveUI()

	results := Apply(
		[]Finding{managedConfigFinding("opencode.jsonc", "model: sonnet\n")},
		ApplyOptions{Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun},
	)

	if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
		t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
	}
	if *answered != 0 {
		t.Errorf("an empty agent asked %d prompts; want none", *answered)
	}
	// The default agent is opencode2; its dir must not be touched.
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "opencode2", "opencode.jsonc"))
}

// TestApplyManagedConfigRefusesAnUnknownAgent pins that an Agent naming no
// registered profile is refused rather than writing into a directory no scope
// reads: the target dir is derived from a real agent's config-dir name.
func TestApplyManagedConfigRefusesAnUnknownAgent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, answered := interactiveUI()

	results := Apply(
		[]Finding{managedConfigFinding("opencode.jsonc", "model: sonnet\n")},
		ApplyOptions{Paths: cp, Slug: testSlug, Agent: "no-such-agent", UI: ui, Mode: options.ModeRun},
	)

	if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
		t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
	}
	if *answered != 0 {
		t.Errorf("an unknown agent asked %d prompts; want none", *answered)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "no-such-agent", "opencode.jsonc"))
}

// unusableConfigDirAgent is a registered stub whose config-dir name is not a
// single path element, so agent.ConfigDirNames rejects it. It drives the
// managed-config write's "no usable config-dir name" refusal, which no built-in
// profile reaches.
type unusableConfigDirAgent struct{ name string }

func (a unusableConfigDirAgent) Name() string             { return a.name }
func (unusableConfigDirAgent) ConfigDirName() string      { return "bad/name" }
func (unusableConfigDirAgent) Experimental() bool         { return false }
func (unusableConfigDirAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// withUnusableConfigDirAgent registers the stub under name for the test,
// restoring the built-in afterward.
func withUnusableConfigDirAgent(t *testing.T, name string) {
	t.Helper()
	original, ok := agent.Lookup(name)
	if !ok {
		t.Fatalf("%s must be a registered agent", name)
	}
	agent.Register(unusableConfigDirAgent{name: name})
	t.Cleanup(func() { agent.Register(original) })
}

// TestApplyManagedConfigRefusesAnUnusableConfigDir pins that a profile declaring
// a config-dir name that is not a single path element is refused: "bad/name"
// would escape the scope directory, so no file is written.
func TestApplyManagedConfigRefusesAnUnusableConfigDir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, answered := interactiveUI()
	withUnusableConfigDirAgent(t, "codex")

	results := Apply(
		[]Finding{managedConfigFinding("config.toml", "model = \"x\"\n")},
		ApplyOptions{Paths: cp, Slug: testSlug, Agent: "codex", UI: ui, Mode: options.ModeRun},
	)

	if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
		t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
	}
	if *answered != 0 {
		t.Errorf("an unusable config dir asked %d prompts; want none", *answered)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "bad", "name", "config.toml"))
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui, answered := interactiveUI()
	cp := configpaths.Get()

	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModePreview,
	})

	if len(results) != 1 || results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcomes = %+v; want one OutcomeSkipped", results)
	}
	if *answered != 0 {
		t.Errorf("the preview asked %d prompts; want none", *answered)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
}

func TestApplyNonInteractivePrintsOneLineAndWritesNothing(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	cp := configpaths.Get()

	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if len(results) != 1 || results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcomes = %+v; want one OutcomeSkipped", results)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Errorf("AcknowledgeCalls = %v; want exactly one line", ui.AcknowledgeCalls)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
}

func TestApplyDeclinedEvidenceIsSkipped(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	finding := perSlugWrite("network.profile", "none")
	finding.EvidenceHash = finding.Evidence.hash()
	if err := state.RecordLearnDecline(testSlug, finding.EvidenceHash); err != nil {
		t.Fatalf("record decline: %v", err)
	}

	ui, answered := interactiveUI()
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcome = %q; want skipped", results[0].Outcome)
	}
	if results[0].Note != "declined for this evidence" {
		t.Errorf("Note = %q; want it to name the recorded decline", results[0].Note)
	}
	if *answered != 0 {
		t.Errorf("a declined finding asked %d prompts; want none", *answered)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
}

func TestApplyPerSlugWritesThroughWriteSetting(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.profile")
	cp := configpaths.Get()
	ui, _ := interactiveUI()

	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	dir := cp.UserSlugConfigDir(testSlug)
	if len(results[0].WroteDirs) != 1 || results[0].WroteDirs[0] != dir {
		t.Errorf("WroteDirs = %v; want the per-slug dir %q", results[0].WroteDirs, dir)
	}
	if content := readFile(t, filepath.Join(dir, "config.yaml")); !strings.Contains(content, "profile: none") {
		t.Errorf("config.yaml = %q; want it to set network.profile to none", content)
	}
}

func TestApplySkipsProjectOwnedKey(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.egress-allow")
	cp := configpaths.Get()

	projectFile := filepath.Join(cp.ProjectConfigDir(), "config.yaml")
	if err := viperconfig.WriteSetting(
		cp.ProjectConfigDir(),
		"network.egress-allow",
		[]string{"api.example"},
	); err != nil {
		t.Fatalf("seed the project config: %v", err)
	}
	before := readFile(t, projectFile)

	ui, answered := interactiveUI()
	results := Apply(
		[]Finding{perSlugWrite("network.egress-allow", []string{"api.example", "new.example"})},
		ApplyOptions{
			Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
		},
	)

	if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
		t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
	}
	if *answered != 0 {
		t.Errorf("a shadowed key asked %d prompts; want none", *answered)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
	if after := readFile(t, projectFile); after != before {
		t.Errorf("project config changed to %q; want it untouched %q", after, before)
	}
}

func TestApplyUnsupportedFormatSkips(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.profile")
	cp := configpaths.Get()

	dir := cp.UserSlugConfigDir(testSlug)
	const json5 = "{\n  // comment\n  \"network\": { \"profile\": \"none\" },\n}\n"
	testutil.WriteFile(t, dir, "config.json5", json5)

	ui, answered := interactiveUI()
	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcome = %q; want skipped", results[0].Outcome)
	}
	if !strings.Contains(results[0].Err, viperconfig.ErrUnsupportedConfigFormat.Error()) {
		t.Errorf("Err = %q; want it to name ErrUnsupportedConfigFormat", results[0].Err)
	}
	if *answered != 0 {
		t.Errorf("an uneditable config asked %d prompts; want none", *answered)
	}
	if content := readFile(t, filepath.Join(dir, "config.json5")); content != json5 {
		t.Errorf("config.json5 = %q; want it untouched", content)
	}
	assertNoFile(t, filepath.Join(dir, "config.yaml"))
}

func TestApplyMoveIsAtomic(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// An unparseable project secret file is refused at plan time, so the whole
	// move skips before its project removal runs.
	projectContent := "OPENAI_API_KEY: [unterminated\n"
	testutil.WriteFile(t, cp.ProjectConfigDir(), "env.secret.yaml", projectContent)

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "OPENAI_API_KEY committed in the project config"},
		Writes: []Write{
			{Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Remove: true}},
			{
				Scope:  ScopePerSlugUser,
				Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Hosts: []string{"api.openai.com"}},
			},
		},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
		SecretValue: func(SecretWrite) (string, bool) { return "sk-secret", true },
	})

	if results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcome = %q; want skipped (the project secret file cannot be edited)", results[0].Outcome)
	}
	if results[0].Err == "" {
		t.Error("Err is empty; want the unparseable secret file's message")
	}
	if !strings.Contains(results[0].Note, results[0].Err) {
		t.Errorf("Note = %q; want it to carry Err = %q", results[0].Note, results[0].Err)
	}
	if content := readFile(t, filepath.Join(cp.ProjectConfigDir(), "env.secret.yaml")); content != projectContent {
		t.Errorf("project secret changed to %q; want it untouched", content)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "env.secret.yaml"))
}

// TestApplyMoveDoesNotLoseProjectSecret pins that a move whose later write cannot
// run is refused before its first write runs: an unparseable per-slug
// env.secret.yaml makes the whole finding skip, so the committed project secret
// is never removed. Validating only at execution time ran the project removal
// first and lost the credential when the per-slug write then failed.
func TestApplyMoveDoesNotLoseProjectSecret(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	projectContent := "OPENAI_API_KEY:\n  value: committed\n  host: api.openai.com\n"
	testutil.WriteFile(t, cp.ProjectConfigDir(), "env.secret.yaml", projectContent)
	// The per-slug target is unparseable, so its write cannot run.
	testutil.WriteFile(t, cp.UserSlugConfigDir(testSlug), "env.secret.yaml", "OPENAI_API_KEY: [unterminated\n")

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "OPENAI_API_KEY committed in the project config"},
		Writes: []Write{
			{Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Remove: true}},
			{
				Scope:  ScopePerSlugUser,
				Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Hosts: []string{"api.openai.com"}},
			},
		},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
		SecretValue: func(SecretWrite) (string, bool) { return "sk-secret", true },
	})

	if results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcome = %q (%s); want skipped (the per-slug write cannot run)", results[0].Outcome, results[0].Err)
	}
	if content := readFile(t, filepath.Join(cp.ProjectConfigDir(), "env.secret.yaml")); content != projectContent {
		t.Errorf("project secret changed to %q; want the committed secret untouched %q", content, projectContent)
	}
}

func TestApplySecretMigratesStaticOnly(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, _ := interactiveUI()

	static := Finding{
		Evidence: Evidence{Summary: "the session sent OPENAI_API_KEY"},
		Writes: []Write{{
			Scope:  ScopePerSlugUser,
			Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Provider: "openai", Hosts: []string{"api.openai.com"}},
		}},
	}
	login := Finding{
		Evidence: Evidence{Summary: "the session used GH_TOKEN"},
		Writes: []Write{{
			Scope:  ScopePerSlugUser,
			Secret: &SecretWrite{EnvVar: "GH_TOKEN", Provider: "github"},
		}},
	}
	loginReads := 0
	results := Apply([]Finding{static, login}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
		SecretValue: func(w SecretWrite) (string, bool) {
			if w.EnvVar == "GH_TOKEN" {
				loginReads++
			}
			return "sk-secret", true
		},
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("static secret outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if dirs := results[0].WroteDirs; len(dirs) != 1 || dirs[0] != cp.UserSlugConfigDir(testSlug) {
		t.Errorf("WroteDirs = %v; want the per-slug dir %q", dirs, cp.UserSlugConfigDir(testSlug))
	}
	if results[1].Outcome != OutcomeSkipped {
		t.Fatalf("login outcome = %q; want skipped", results[1].Outcome)
	}
	if loginReads != 0 {
		t.Errorf("a login credential's value was read %d times; want never", loginReads)
	}
	content := readFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "env.secret.yaml"))
	if !strings.Contains(content, "OPENAI_API_KEY") || !strings.Contains(content, "api.openai.com") {
		t.Errorf("env.secret.yaml = %q; want the static secret with its host", content)
	}
	if !strings.Contains(content, "sk-secret") {
		t.Errorf("env.secret.yaml = %q; want the static value", content)
	}
	if strings.Contains(content, "GH_TOKEN") {
		t.Errorf("env.secret.yaml = %q; want no login credential", content)
	}
	if strings.Contains(content, "allow_any_host_dangerous") {
		t.Errorf("env.secret.yaml = %q; want no allow_any_host_dangerous", content)
	}
}

// TestApplySecretSetsThePlaceholder pins that an applied static secret carries
// the guest-visible placeholder for its env var, so the managed config's
// ${ENVVAR} is the placeholder the runtime substitutes rather than an undefined
// reference.
func TestApplySecretSetsThePlaceholder(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, _ := interactiveUI()

	finding := Finding{
		Evidence: Evidence{Summary: "the session sent OPENAI_API_KEY"},
		Writes: []Write{{
			Scope:  ScopePerSlugUser,
			Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Provider: "openai", Hosts: []string{"api.openai.com"}},
		}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
		SecretValue: func(SecretWrite) (string, bool) { return "sk-secret", true },
	})
	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}

	content := readFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "env.secret.yaml"))
	placeholder := envsecret.PlaceholderFor("OPENAI_API_KEY")
	if !strings.Contains(content, "placeholder: "+placeholder) {
		t.Errorf("env.secret.yaml = %q; want placeholder %s", content, placeholder)
	}
}

func TestApplyProjectScopeIsGated(t *testing.T) {
	t.Run("project scope extends the approval", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		unsetKeyEnvironment(t, "network.profile")
		cp := configpaths.Get()
		if err := viperconfig.WriteSetting(cp.ProjectConfigDir(), "network.profile", "broad"); err != nil {
			t.Fatalf("seed the project config: %v", err)
		}
		approveProjectLayer(t, cp)

		ui, _ := interactiveUI()
		finding := Finding{
			Evidence: Evidence{Summary: "the session narrowed the profile"},
			Writes:   []Write{{Scope: ScopeProject, ConfigKey: "network.profile", ConfigValue: "none"}},
		}
		results := Apply([]Finding{finding}, ApplyOptions{
			Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
		})

		if results[0].Outcome != OutcomeApplied {
			t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
		}
		if dirs := results[0].WroteDirs; len(dirs) != 1 || dirs[0] != cp.ProjectConfigDir() {
			t.Errorf("WroteDirs = %v; want the project config dir %q", dirs, cp.ProjectConfigDir())
		}
		if !projectLayerApproved(t, cp) {
			t.Error(
				"a project-scope write left the project config needing approval; ApproveSettingWrite was not called",
			)
		}
	})

	t.Run("per-slug scope records no approval", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		unsetKeyEnvironment(t, "network.profile")
		cp := configpaths.Get()

		ui, _ := interactiveUI()
		results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
			Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
		})

		if results[0].Outcome != OutcomeApplied {
			t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
		}
		if _, found, err := projecttrust.LoadApproval(testCheckoutSlug); err != nil {
			t.Fatalf("load approval: %v", err)
		} else if found {
			t.Error("a per-slug write recorded a project approval; the trust gate must not run for it")
		}
	})
}

func TestApplyNeverShadowedByFlagOrEnv(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		cp := configpaths.Get()
		ui, answered := interactiveUI()

		results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
			Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
			SetByFlag: func(key string) bool { return key == "network.profile" },
		})

		if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
			t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
		}
		if *answered != 0 {
			t.Errorf("a flag-shadowed key asked %d prompts; want none", *answered)
		}
		assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
	})

	t.Run("environment", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		cp := configpaths.Get()
		t.Setenv(viperconfig.EnvVarNames("network.profile")[0], "none")
		ui, _ := interactiveUI()

		results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
			Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
		})

		if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
			t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
		}
		assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
	})

	t.Run("flag predicate false writes", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		unsetKeyEnvironment(t, "network.profile")
		cp := configpaths.Get()
		ui, _ := interactiveUI()

		results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
			Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
			SetByFlag: func(string) bool { return false },
		})

		if results[0].Outcome != OutcomeApplied {
			t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
		}
	})
}

func TestApplyAssumeYesDoesNotAccept(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	ui := decliningUI()
	ui.SetAssumeYes(true)
	ui.SelectFn = nil // Select is not answered by assume-yes; it takes its default.

	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeDeclined {
		t.Fatalf("outcome = %q; want declined (assume-yes must not answer the prompt)", results[0].Outcome)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
}

// TestApplyAcceptAllSkipsThePrompt pins the top-level "Save all": with AcceptAll
// set, every writable finding is applied without a per-finding Select.
func TestApplyAcceptAllSkipsThePrompt(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.profile")
	cp := configpaths.Get()
	ui, answered := interactiveUI()

	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun, AcceptAll: true,
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if *answered != 0 {
		t.Errorf("AcceptAll asked %d prompts; want none", *answered)
	}
}

// TestApplyAcceptAllStillRefusesAShadowedKey pins that AcceptAll still runs the
// write planner: a project-owned key is refused, never applied.
func TestApplyAcceptAllStillRefusesAShadowedKey(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.egress-allow")
	cp := configpaths.Get()
	if err := viperconfig.WriteSetting(
		cp.ProjectConfigDir(),
		"network.egress-allow",
		[]string{"api.example"},
	); err != nil {
		t.Fatalf("seed the project config: %v", err)
	}

	ui, answered := interactiveUI()
	results := Apply(
		[]Finding{perSlugWrite("network.egress-allow", []string{"api.example", "new.example"})},
		ApplyOptions{Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun, AcceptAll: true},
	)

	if results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcome = %q; want skipped for a shadowed key", results[0].Outcome)
	}
	if *answered != 0 {
		t.Errorf("a shadowed key asked %d prompts; want none", *answered)
	}
}

// TestApplyPromptSurfacesWriteText: a write's free text is part of the question
// the user accepts, so a proposal that must state a fact renders it.
func TestApplyPromptSurfacesWriteText(t *testing.T) {
	finding := Finding{
		Evidence: Evidence{Summary: "the session used api.example"},
		Writes: []Write{{
			Scope:     ScopePerSlugUser,
			ConfigKey: "network.egress-allow",
			Text:      "The network policy is creation-only.",
		}},
	}

	prompt := applyPrompt(finding)

	if !strings.Contains(prompt, "the session used api.example") {
		t.Errorf("prompt = %q; want the evidence summary", prompt)
	}
	if !strings.Contains(prompt, "The network policy is creation-only.") {
		t.Errorf("prompt = %q; want the write's text", prompt)
	}
	if !strings.Contains(prompt, "Apply this change?") {
		t.Errorf("prompt = %q; want the final question line", prompt)
	}
}

// TestApplyPromptListsTheProposedDestinations: a proposed egress-allow entry
// names the destination, the use behind it, and the channel that saw it, so the
// user can judge the lock before applying it. Denied destinations are listed
// apart, because they are never allowed.
func TestApplyPromptListsTheProposedDestinations(t *testing.T) {
	finding := Finding{
		Kind: KindObservation,
		Writes: []Write{{
			Scope:       ScopePerSlugUser,
			ConfigKey:   "network.egress-allow",
			ConfigValue: []string{"already.example", "api.anthropic.com", "raw.githubusercontent.com"},
		}},
		Evidence: Evidence{
			Summary:      "lock egress to the destinations this session used",
			Destinations: []string{"api.anthropic.com", "raw.githubusercontent.com"},
			Denied:       []string{"evil.example"},
			Count:        12,
			Succeeded:    true,
			Channel:      "proxy",
		},
	}

	prompt := applyPrompt(finding)

	for _, want := range []string{
		"  + api.anthropic.com",
		"  + raw.githubusercontent.com",
		"12 requests observed by the proxy",
		"  - evil.example",
		"  = already.example (already allowed)",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt = %q; want it to contain %q", prompt, want)
		}
	}
	if strings.Index(prompt, "+ api.anthropic.com") > strings.Index(prompt, "Apply this change?") {
		t.Errorf("prompt = %q; want the destinations before the question", prompt)
	}
}

func TestApplyPromptOmitsAZeroRequestCount(t *testing.T) {
	finding := Finding{
		Kind: KindObservation,
		Writes: []Write{
			{Scope: ScopePerSlugUser, ConfigKey: "network.egress-allow", ConfigValue: []string{"tunnel-only.example"}},
		},
		Evidence: Evidence{Destinations: []string{"tunnel-only.example"}, Channel: "proxy"},
	}

	prompt := applyPrompt(finding)

	if strings.Contains(prompt, "0 requests") || !strings.Contains(prompt, "Allowed (observed by the proxy):") {
		t.Errorf("prompt = %q; want the channel without a zero request count", prompt)
	}
}

// TestApplyPromptListsDestinationsOnlyForTheEgressLock: a secret or a config
// finding also carries destinations (the hosts a secret is substituted for), but
// they are not allowed egress, so only the lock renders the allow list.
func TestApplyPromptListsDestinationsOnlyForTheEgressLock(t *testing.T) {
	finding := Finding{
		Evidence: Evidence{Summary: "save the key", Destinations: []string{"api.example"}, Channel: "readback"},
		Writes:   []Write{{Scope: ScopePerSlugUser, ConfigKey: "secrets"}},
	}

	prompt := applyPrompt(finding)

	if strings.Contains(prompt, "Allowed") || strings.Contains(prompt, "  + ") {
		t.Errorf("prompt = %q; want no allow list for a finding that writes no egress-allow", prompt)
	}
}

// TestApplyPromptOmitsTheDestinationBlockForAConfigFinding: config harden's
// wildcard drop also writes network.egress-allow and carries the remaining
// entries as destinations, but nothing was observed, so no allow list renders.
func TestApplyPromptOmitsTheDestinationBlockForAConfigFinding(t *testing.T) {
	finding := Finding{
		Kind: KindConfig,
		Evidence: Evidence{
			Summary:      "the egress-allow list contains a wildcard entry: *",
			Destinations: []string{"api.example"},
			Channel:      "config",
		},
		Writes: []Write{{Scope: ScopePerSlugUser, ConfigKey: egressAllowKey, ConfigValue: []string{"api.example"}}},
	}

	prompt := applyPrompt(finding)

	if strings.Contains(prompt, "Allowed") || strings.Contains(prompt, "  + ") {
		t.Errorf("prompt = %q; want no allow list for a config finding", prompt)
	}
}

// TestApplyPromptOfTheInterceptionFindingHasNoAllowList: the interception-only
// finding writes no allow list, so its prompt lists no destinations.
func TestApplyPromptOfTheInterceptionFindingHasNoAllowList(t *testing.T) {
	finding := interceptionFinding(network.Policy{}, []string{"api.example"})

	prompt := applyPrompt(finding)

	if strings.Contains(prompt, "Allowed") || strings.Contains(prompt, "  + ") {
		t.Errorf("prompt = %q; want no allow list for the interception-only finding", prompt)
	}
}

func TestApplyPromptOmitsTheDestinationBlockWithoutDestinations(t *testing.T) {
	finding := Finding{Evidence: Evidence{Summary: "move the key to env.secret.yaml"}}

	prompt := applyPrompt(finding)

	if strings.Contains(prompt, "requests observed") || strings.Contains(prompt, "  + ") {
		t.Errorf("prompt = %q; want no destination block for a finding without destinations", prompt)
	}
}

// TestApplyFailsWhenTheDeclineMemoryCannotBeRead pins that an unreadable decline
// memory is a failure, not a silent re-proposal: the applier must not ask about a
// finding whose decline state it could not determine.
func TestApplyFailsWhenTheDeclineMemoryCannotBeRead(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	stateDir := cp.UserStateDir()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("mkdir state root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.yaml"), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}

	finding := perSlugWrite("network.profile", "none")
	finding.EvidenceHash = finding.Evidence.hash()
	ui, answered := interactiveUI()
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeFailed || results[0].Err == "" {
		t.Fatalf("result = %+v; want a failed outcome carrying the read error", results[0])
	}
	if *answered != 0 {
		t.Errorf("the unreadable decline memory asked %d prompts; want none", *answered)
	}
}

// TestApplyFailsWhenThePromptFails pins that a prompt error fails the finding
// rather than being read as a decline.
func TestApplyFailsWhenThePromptFails(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return "", errors.New("prompt failed")
	}

	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeFailed || !strings.Contains(results[0].Err, "ask to apply the finding") {
		t.Fatalf("result = %+v; want a failed outcome naming the prompt error", results[0])
	}
}

// TestApplyReportsAFailedDeclineRecord pins that a decline the applier could not
// persist is surfaced: the finding is declined, but the run reports that the
// memory was not updated.
func TestApplyReportsAFailedDeclineRecord(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	restore := state.OverrideStateWriteForTest(func(string, []byte) error {
		return errors.New("state write boom")
	})
	t.Cleanup(restore)

	finding := perSlugWrite("network.profile", "none")
	finding.EvidenceHash = finding.Evidence.hash()
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: decliningUI(), Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeDeclined || results[0].Err == "" {
		t.Fatalf("result = %+v; want a declined outcome carrying the record error", results[0])
	}
}

// TestApplySkipsAProjectWriteWithoutACheckoutSlug pins that a project-scope
// write the applier cannot gate is refused before any prompt or write.
func TestApplySkipsAProjectWriteWithoutACheckoutSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, answered := interactiveUI()

	finding := Finding{
		Evidence: Evidence{Summary: "narrow the profile"},
		Writes:   []Write{{Scope: ScopeProject, ConfigKey: "network.profile", ConfigValue: "none"}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
		t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
	}
	if *answered != 0 {
		t.Errorf("a project write with no checkout slug asked %d prompts; want none", *answered)
	}
}

// TestApplySkipsASecretWithNoResolvableValue pins that a static secret whose
// value the applier cannot resolve is refused, and that a nil resolver reports
// the same: a login credential's value is never read.
func TestApplySkipsASecretWithNoResolvableValue(t *testing.T) {
	t.Run("nil resolver", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		cp := configpaths.Get()
		ui, _ := interactiveUI()

		results := Apply([]Finding{secretFindingFor("OPENAI_API_KEY")}, ApplyOptions{
			Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
		})

		if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
			t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
		}
	})

	t.Run("empty value", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		cp := configpaths.Get()
		ui, _ := interactiveUI()

		results := Apply([]Finding{secretFindingFor("OPENAI_API_KEY")}, ApplyOptions{
			Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
			SecretValue: func(SecretWrite) (string, bool) { return "", true },
		})

		if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
			t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
		}
	})
}

// TestApplySkipsAConfigDirItCannotRead pins that a target directory whose config
// file cannot be discovered is refused before any prompt: an approved config file
// that has gone missing is a refusal, not an absent file to create.
func TestApplySkipsAConfigDirItCannotRead(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	dir := cp.UserSlugConfigDir(testSlug)
	gone := filepath.Join(dir, "config.yaml")
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{gone: projectguard.ApprovalOf([]byte("memory: 1G\n"))},
	))

	ui, answered := interactiveUI()
	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeSkipped || results[0].Err == "" {
		t.Fatalf("result = %+v; want a skipped outcome carrying the read error", results[0])
	}
	if !strings.Contains(results[0].Note, "cannot be read for editing") {
		t.Errorf("Note = %q; want the read failure, not a format mismatch", results[0].Note)
	}
	if *answered != 0 {
		t.Errorf("an unreadable config dir asked %d prompts; want none", *answered)
	}
}

// TestApplyRemovesAConfigKey pins the removal form: a per-slug finding whose
// write removes a key deletes it through RemoveSetting.
func TestApplyRemovesAConfigKey(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.profile")
	cp := configpaths.Get()
	dir := cp.UserSlugConfigDir(testSlug)
	testutil.WriteFile(t, dir, "config.yaml", "network:\n  profile: none\n")

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "narrow the profile"},
		Writes:   []Write{{Scope: ScopePerSlugUser, ConfigKey: "network.profile", Remove: true}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if dirs := results[0].WroteDirs; len(dirs) != 1 || dirs[0] != dir {
		t.Errorf("WroteDirs = %v; want the per-slug dir %q", dirs, dir)
	}
	if content := readFile(t, filepath.Join(dir, "config.yaml")); strings.Contains(content, "profile") {
		t.Errorf("config.yaml = %q; want network.profile removed", content)
	}
}

// TestApplyRemovesASecret pins the removal form for a secret: a finding whose
// write removes a static credential deletes it through RemoveSecretSpec.
func TestApplyRemovesASecret(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	dir := cp.UserSlugConfigDir(testSlug)
	testutil.WriteFile(t, dir, "env.secret.yaml", "OPENAI_API_KEY:\n  value: v\n  host: api.openai\n")

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "remove the committed secret"},
		Writes:   []Write{{Scope: ScopePerSlugUser, Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Remove: true}}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if content := readFile(t, filepath.Join(dir, "env.secret.yaml")); strings.Contains(content, "OPENAI_API_KEY") {
		t.Errorf("env.secret.yaml = %q; want the secret removed", content)
	}
}

// TestApplyRemovesAProjectSetting pins the project-scope removal: it extends the
// approval, removes the key through the trust gate, and leaves the file without
// it.
func TestApplyRemovesAProjectSetting(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.profile")
	cp := configpaths.Get()
	if err := viperconfig.WriteSetting(cp.ProjectConfigDir(), "network.profile", "broad"); err != nil {
		t.Fatalf("seed the project config: %v", err)
	}
	approveProjectLayer(t, cp)

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "narrow the profile"},
		Writes:   []Write{{Scope: ScopeProject, ConfigKey: "network.profile", Remove: true}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if content := readFile(
		t,
		filepath.Join(cp.ProjectConfigDir(), "config.yaml"),
	); strings.Contains(
		content,
		"profile",
	) {
		t.Errorf("project config = %q; want network.profile removed", content)
	}
}

// TestApplyProjectSecretMoveKeepsTheApproval pins that moving a project secret
// (C1's removal) extends the persistent checkout approval by the removed
// env-var, so the next run does not gate the launcher's own removal. Without
// ApproveSecretWrite the recorded approval still holds the removed secret and
// the change re-prompts.
func TestApplyProjectSecretMoveKeepsTheApproval(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	// A second project input keeps the projection non-empty after the secret is
	// removed, so the removal is a real change the approval must absorb (an empty
	// projection is approved regardless).
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "network:\n  profile: none\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "env.secret.yaml",
		"WIDGET_TOKEN:\n  value: sk-project\n  host: api.widget.example\n")
	approveProjectLayer(t, cp)

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "WIDGET_TOKEN is committed in the project scope"},
		Writes: []Write{
			{Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "WIDGET_TOKEN", Remove: true}},
			{
				Scope:  ScopePerSlugUser,
				Secret: &SecretWrite{EnvVar: "WIDGET_TOKEN", Hosts: []string{"api.widget.example"}},
			},
		},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
		SecretValue: func(SecretWrite) (string, bool) { return "sk-project", true },
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if !projectLayerApproved(t, cp) {
		t.Error(
			"moving a project secret left the project config needing approval; ApproveSecretWrite was not called",
		)
	}
}

// TestApplyFailsWhenTheProjectApprovalCannotBeRecorded pins that a project-scope
// write fails when its approval cannot be extended: the write must not run
// without the approval that would keep the next run from re-asking.
func TestApplyFailsWhenTheProjectApprovalCannotBeRecorded(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.profile")
	cp := configpaths.Get()
	stateDir := cp.UserStateDir()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("mkdir state root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.yaml"), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "narrow the profile"},
		Writes:   []Write{{Scope: ScopeProject, ConfigKey: "network.profile", ConfigValue: "none"}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeFailed || results[0].Err == "" {
		t.Fatalf("result = %+v; want a failed outcome carrying the approval error", results[0])
	}
}

// TestApplyRestoresTheApprovalWhenTheProjectWriteFails pins that a project write
// that fails after its approval was extended restores the previous approval, so
// the launcher's own failed change does not need re-approval.
func TestApplyRestoresTheApprovalWhenTheProjectWriteFails(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.profile")
	cp := configpaths.Get()
	// The key's prefix is a scalar, so setting the nested key cannot be
	// expressed and the write fails after the approval is extended.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "network: private\n")
	approveProjectLayer(t, cp)
	before := readFile(t, filepath.Join(cp.ProjectConfigDir(), "config.yaml"))

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "narrow the profile"},
		Writes:   []Write{{Scope: ScopeProject, ConfigKey: "network.profile", ConfigValue: "none"}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeFailed {
		t.Fatalf("outcome = %q (%s); want a failed write", results[0].Outcome, results[0].Err)
	}
	if after := readFile(t, filepath.Join(cp.ProjectConfigDir(), "config.yaml")); after != before {
		t.Errorf("project config changed to %q; want it untouched %q", after, before)
	}
}

// TestApplyNonStringProjectWriteKeepsTheApproval pins the trust round-trip for a
// non-string project value: the recorded approval entry must equal the
// projection's own rendering, or the next run gates the launcher's own change.
// A boolean (C6's provision-host-config) and a list (C4's egress-allow) are the
// shapes a project-scope harden write can carry.
func TestApplyNonStringProjectWriteKeepsTheApproval(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		value   any
		written string
	}{
		{name: "boolean", key: "provision-host-config", value: false, written: "provision-host-config: false"},
		{
			name:    "list",
			key:     "network.egress-allow",
			value:   []string{"api.example"},
			written: "egress-allow:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			unsetKeyEnvironment(t, tc.key)
			cp := configpaths.Get()
			testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "agent: opencode\n")
			approveProjectLayer(t, cp)

			ui, _ := interactiveUI()
			finding := Finding{
				Evidence: Evidence{Summary: "harden " + tc.key},
				Writes:   []Write{{Scope: ScopeProject, ConfigKey: tc.key, ConfigValue: tc.value}},
			}
			results := Apply([]Finding{finding}, ApplyOptions{
				Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
			})

			if results[0].Outcome != OutcomeApplied {
				t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
			}
			if !projectLayerApproved(t, cp) {
				t.Error("a project-scope write left the project config needing approval")
			}
			written := readFile(t, filepath.Join(cp.ProjectConfigDir(), "config.yaml"))
			if !strings.Contains(written, tc.written) {
				t.Errorf("project config = %q, want it to contain %q", written, tc.written)
			}
		})
	}
}

// secretFindingFor builds a one-write finding proposing a static secret with a
// derivable host.
func secretFindingFor(envVar string) Finding {
	return Finding{
		Evidence: Evidence{Summary: "the session used " + envVar},
		Writes: []Write{{
			Scope:  ScopePerSlugUser,
			Secret: &SecretWrite{EnvVar: envVar, Provider: "openai", Hosts: []string{"api.openai.com"}},
		}},
	}
}

// approveProjectLayer records the current project config layer as approved.
func approveProjectLayer(t *testing.T, cp configpaths.ConfigPaths) {
	t.Helper()
	projection, err := projecttrust.Compute(projecttrust.Sources{ProjectConfigDir: cp.ProjectConfigDirAbs()})
	if err != nil {
		t.Fatalf("compute projection: %v", err)
	}
	if err := projecttrust.RecordApproval(testCheckoutSlug, projection); err != nil {
		t.Fatalf("record approval: %v", err)
	}
}

// projectLayerApproved reports whether the current project config layer is the
// approved one.
func projectLayerApproved(t *testing.T, cp configpaths.ConfigPaths) bool {
	t.Helper()
	projection, err := projecttrust.Compute(projecttrust.Sources{ProjectConfigDir: cp.ProjectConfigDirAbs()})
	if err != nil {
		t.Fatalf("compute projection: %v", err)
	}
	approval, _, err := projecttrust.LoadApproval(testCheckoutSlug)
	if err != nil {
		t.Fatalf("load approval: %v", err)
	}
	return projecttrust.Evaluate(projection, approval).Approved
}

// TestApplyWithoutAUIReportsNoInteractiveTerminal pins that a missing UI is
// treated as a non-interactive run: the applier asks nothing and writes nothing
// rather than dereferencing the nil terminal.
func TestApplyWithoutAUIReportsNoInteractiveTerminal(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: nil, Mode: options.ModeRun,
	})

	if len(results) != 1 || results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcomes = %+v; want one OutcomeSkipped", results)
	}
	if results[0].Note != "no interactive terminal" {
		t.Errorf("Note = %q; want the non-interactive notice", results[0].Note)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
}

// TestApplyDecliningAnUnhashedFindingRecordsNoDecline pins that a finding with
// no evidence hash is declined without a decline record: there is no evidence
// to key one on, so the next run proposes it again.
func TestApplyDecliningAnUnhashedFindingRecordsNoDecline(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	results := Apply([]Finding{perSlugWrite("network.profile", "none")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: decliningUI(), Mode: options.ModeRun,
	})
	if results[0].Outcome != OutcomeDeclined {
		t.Fatalf("outcome = %q; want declined", results[0].Outcome)
	}

	declined, err := state.LearnDeclined(testSlug, "")
	if err != nil {
		t.Fatalf("LearnDeclined: %v", err)
	}
	if declined {
		t.Error("an empty evidence hash was recorded as a decline; want no record")
	}
}

// TestApplySecretWriteIsNeverShadowedByAProjectOwnedKey pins that a secret write
// targets the per-slug directory directly: it is not a config-key write, so the
// ownership scan never redirects or refuses it, even when the project config
// holds a key the ownership scan would resolve.
func TestApplySecretWriteIsNeverShadowedByAProjectOwnedKey(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	// An empty key is the only key the ownership scan resolves for a bare
	// secret write (whose ConfigKey is ""); the secret write must not consult it.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "\"\": 1\n")

	dir, refusal := planTarget(
		Write{
			Scope:  ScopePerSlugUser,
			Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Hosts: []string{"api.example"}},
		},
		ApplyOptions{Paths: cp, Slug: testSlug},
	)
	if refusal != nil {
		t.Fatalf("refusal = %+v; want none for a secret write", refusal)
	}
	if dir != cp.UserSlugConfigDir(testSlug) {
		t.Errorf("dir = %q; want the per-slug dir %q", dir, cp.UserSlugConfigDir(testSlug))
	}
}

// emptyConfigPaths resolves no directories, so a per-slug write's ownership scan
// finds no owner and the project dir shares the empty owner string.
type emptyConfigPaths struct{}

func (emptyConfigPaths) UserConfigDir() string            { return "" }
func (emptyConfigPaths) UserSlugConfigDir(string) string  { return "" }
func (emptyConfigPaths) UserCacheDir() string             { return "" }
func (emptyConfigPaths) UserStateDir() string             { return "" }
func (emptyConfigPaths) UserDataDir() string              { return "" }
func (emptyConfigPaths) UserHomeDir() (string, error)     { return "", nil }
func (emptyConfigPaths) ProjectConfigDir() string         { return "" }
func (emptyConfigPaths) ProjectConfigDirAbs() string      { return "" }
func (emptyConfigPaths) ProjectDockerfile() string        { return "" }
func (emptyConfigPaths) IgnoredWorktreeConfigDir() string { return "" }

// TestPlanTargetKeepsAPerSlugWriteWithNoResolvedOwner pins that a per-slug
// config write is allowed when no file layer owns its key, even when the
// project dir is unresolved and shares the empty owner string: an unresolved
// path must not read as a project-owned key.
func TestPlanTargetKeepsAPerSlugWriteWithNoResolvedOwner(t *testing.T) {
	_, refusal := planTarget(
		Write{Scope: ScopePerSlugUser, ConfigKey: "network.profile", ConfigValue: "none"},
		ApplyOptions{Paths: emptyConfigPaths{}, Slug: testSlug},
	)
	if refusal != nil {
		t.Fatalf("refusal = %+v; want none when no layer owns the key", refusal)
	}
}

// TestApplySkipsASecretWhoseResolverReportsNoValue pins that a secret is refused
// whenever the resolver does not positively supply a value, even if it returns a
// non-empty string alongside ok=false: only ok authorizes the write.
func TestApplySkipsASecretWhoseResolverReportsNoValue(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, answered := interactiveUI()

	results := Apply([]Finding{secretFindingFor("OPENAI_API_KEY")}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
		SecretValue: func(SecretWrite) (string, bool) { return "sk-never-written", false },
	})

	if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
		t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
	}
	if *answered != 0 {
		t.Errorf("a secret with no resolvable value asked %d prompts; want none", *answered)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "env.secret.yaml"))
}

// TestResolveSecretValueWithoutAResolverReportsNoValue pins the nil-resolver
// contract at the helper: no resolver means no value, not an empty-string value.
func TestResolveSecretValueWithoutAResolverReportsNoValue(t *testing.T) {
	value, ok := resolveSecretValue(&SecretWrite{EnvVar: "OPENAI_API_KEY"}, ApplyOptions{})
	if ok || value != "" {
		t.Fatalf("resolveSecretValue = (%q, %v); want no value", value, ok)
	}
}

// TestConfigDirEditableReportsAReadError pins that a config directory whose file
// cannot be discovered is reported uneditable with the read error, not as a
// format mismatch.
func TestConfigDirEditableReportsAReadError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	dir := configpaths.Get().UserSlugConfigDir(testSlug)
	gone := filepath.Join(dir, "config.yaml")
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{gone: projectguard.ApprovalOf([]byte("memory: 1G\n"))},
	))

	editable, err := configDirEditable(dir)
	if editable || err == nil {
		t.Fatalf("configDirEditable = (%v, %v); want false and the read error", editable, err)
	}
}

// TestAskApplyReportsThePromptError pins that a failed prompt is returned as a
// failure with its cause preserved, and never read as an acceptance.
func TestAskApplyReportsThePromptError(t *testing.T) {
	sentinel := errors.New("prompt boom")
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "", sentinel }

	accepted, err := askApply(ui, perSlugWrite("network.profile", "none"))
	if accepted {
		t.Error("askApply accepted = true; want false when the prompt fails")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("askApply error = %v; want it to wrap the prompt error", err)
	}
}

// fileModeWriteFor builds a one-write finding proposing a mode change on a
// scope's env.secret.yaml.
func fileModeWriteFor(scope Scope) Finding {
	return Finding{
		Evidence: Evidence{Summary: "the secret file is group- or world-readable"},
		Writes: []Write{{
			Scope:    scope,
			FileMode: &FileModeWrite{Mode: 0o600},
			Text:     "Tighten env.secret.yaml to 0600.",
		}},
	}
}

// fileMode returns the permission bits of path.
func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// TestApplyFileModeChmodsTheSecretFile pins the file-mode write: a per-slug
// env.secret.yaml with a broad mode is chmodded to the write's mode, and no
// content is touched.
func TestApplyFileModeChmodsTheSecretFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	dir := cp.UserSlugConfigDir(testSlug)
	const content = "OPENAI_API_KEY:\n  value: v\n  host: api.openai.com\n"
	testutil.WriteFile(t, dir, "env.secret.yaml", content)
	secretPath := filepath.Join(dir, "env.secret.yaml")
	if err := os.Chmod(secretPath, 0o644); err != nil {
		t.Fatalf("chmod the seed to 0644: %v", err)
	}

	ui, _ := interactiveUI()
	results := Apply([]Finding{fileModeWriteFor(ScopePerSlugUser)}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if dirs := results[0].WroteDirs; len(dirs) != 1 || dirs[0] != dir {
		t.Errorf("WroteDirs = %v; want the per-slug dir %q", dirs, dir)
	}
	if mode := fileMode(t, secretPath); mode != 0o600 {
		t.Errorf("env.secret.yaml mode = %o; want %o", mode, 0o600)
	}
	if got := readFile(t, secretPath); got != content {
		t.Errorf("env.secret.yaml = %q; want its content untouched %q", got, content)
	}
}

// TestApplyFileModeSkipsAnAbsentFile pins that the file-mode write never creates
// a file to chmod: an absent env.secret.yaml is skipped and nothing is written.
func TestApplyFileModeSkipsAnAbsentFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	secretPath := filepath.Join(cp.UserSlugConfigDir(testSlug), "env.secret.yaml")

	ui, answered := interactiveUI()
	results := Apply([]Finding{fileModeWriteFor(ScopePerSlugUser)}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeSkipped || results[0].Note == "" {
		t.Fatalf("result = %+v; want a skipped outcome with a note", results[0])
	}
	if *answered != 0 {
		t.Errorf("an absent secret file asked %d prompts; want none", *answered)
	}
	assertNoFile(t, secretPath)
}

// TestApplyFileModeProjectScopeIsGated pins that a project-scope file-mode write
// goes through the project trust gate: an unapproved project env.secret.yaml is
// refused (the guard blocks its read) and its mode is left unchanged.
func TestApplyFileModeProjectScopeIsGated(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	projectDir := cp.ProjectConfigDir()
	testutil.WriteFile(t, projectDir, "env.secret.yaml", "OPENAI_API_KEY:\n  value: v\n  host: api.openai.com\n")
	secretPath := filepath.Join(projectDir, "env.secret.yaml")
	if err := os.Chmod(secretPath, 0o644); err != nil {
		t.Fatalf("chmod the seed to 0644: %v", err)
	}
	// The project scope is guarded but not approved, so the guard refuses the
	// secret file's read and the write is a refusal, not a chmod.
	t.Cleanup(projectguard.Install([]string{projectDir}, nil))

	ui, answered := interactiveUI()
	results := Apply([]Finding{fileModeWriteFor(ScopeProject)}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeSkipped {
		t.Fatalf("outcome = %q (%s); want skipped for the unapproved project scope", results[0].Outcome, results[0].Err)
	}
	if *answered != 0 {
		t.Errorf("an unapproved project secret asked %d prompts; want none", *answered)
	}
	if mode := fileMode(t, secretPath); mode != 0o644 {
		t.Errorf("env.secret.yaml mode = %o; want it unchanged %o", mode, 0o644)
	}
}

// TestApplyProjectRemovalFailureRestoresTheApproval pins that a project-scope
// removal that fails after its approval was extended reports the failure and
// restores the previous approval, so the failed change never leaves the project
// config needing re-approval.
func TestApplyProjectRemovalFailureRestoresTheApproval(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	unsetKeyEnvironment(t, "network.profile")
	cp := configpaths.Get()
	// The anchored value is referenced by an alias, so the document edit refuses
	// the removal after the approval is extended.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "network: &n private\nmirror: *n\n")
	approveProjectLayer(t, cp)

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "narrow the profile"},
		Writes:   []Write{{Scope: ScopeProject, ConfigKey: "network", Remove: true}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeFailed || results[0].Err == "" {
		t.Fatalf("result = %+v; want a failed removal carrying the error", results[0])
	}
	if !projectLayerApproved(t, cp) {
		t.Error("a failed project removal left the extended approval in place; restore() was not called")
	}
}

// TestApplySkipsATextOnlyWrite pins that a write naming no config key, secret,
// or file mode makes no machine change: it is rendered in the prompt but never
// planned or executed, so it cannot create a stray config file. The finding's
// actionable write still applies.
func TestApplySkipsATextOnlyWrite(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	finding := Finding{
		Evidence: Evidence{Summary: "an inline secret is in the managed agent config"},
		Writes: []Write{
			{
				Scope:  ScopePerSlugUser,
				Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Provider: "openai", Hosts: []string{"api.openai.com"}},
				Text:   "move the value to env.secret.yaml",
			},
			{Scope: ScopePerSlugUser, Text: "replace the literal with ${OPENAI_API_KEY}"},
		},
	}

	ui, answered := interactiveUI()
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
		SecretValue: func(SecretWrite) (string, bool) { return "sk-inline", true },
	})

	if results[0].Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q (%s); want applied", results[0].Outcome, results[0].Err)
	}
	if *answered != 1 {
		t.Errorf("the finding asked %d prompts; want exactly one", *answered)
	}
	if got := readFile(
		t,
		filepath.Join(cp.UserSlugConfigDir(testSlug), "env.secret.yaml"),
	); !strings.Contains(
		got,
		"OPENAI_API_KEY",
	) {
		t.Errorf("env.secret.yaml = %q; want the moved secret", got)
	}
	assertNoFile(t, filepath.Join(cp.UserSlugConfigDir(testSlug), "config.yaml"))
}

// TestApplyReportsAWriteLessFindingWithoutPrompting pins that a finding that
// plans no write (a report-only finding, e.g. C2 with no host or C4 dropping the
// last wildcard) is neither prompted nor counted as applied: it is reported as
// not applicable and nothing is declined.
func TestApplyReportsAWriteLessFindingWithoutPrompting(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	reportOnly := Finding{
		Evidence:     Evidence{Summary: "OPENAI_API_KEY allows any host with no host to narrow to"},
		EvidenceHash: "report-only",
	}

	ui, answered := interactiveUI()
	results := Apply([]Finding{reportOnly}, ApplyOptions{
		Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeNotApplicable {
		t.Fatalf("outcome = %q (%s); want not-applicable", results[0].Outcome, results[0].Err)
	}
	if !strings.Contains(results[0].Note, "reported only") {
		t.Errorf("note = %q; want it to say the finding is reported only", results[0].Note)
	}
	if *answered != 0 {
		t.Errorf("a write-less finding asked %d prompts; want none", *answered)
	}
	declined, err := state.LearnDeclined(testSlug, "report-only")
	if err != nil {
		t.Fatalf("LearnDeclined: %v", err)
	}
	if declined {
		t.Error("a write-less finding recorded a decline")
	}
}

// TestApplyFailsWhenTheProjectSecretApprovalCannotBeRecorded pins that a
// project-scope secret removal fails when its approval cannot be extended: the
// removal must not run without the approval that keeps the next run from asking
// about the launcher's own change.
func TestApplyFailsWhenTheProjectSecretApprovalCannotBeRecorded(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "network:\n  profile: none\n")
	approveProjectLayer(t, cp)
	restore := state.OverrideStateWriteForTest(func(string, []byte) error { return errors.New("boom") })
	t.Cleanup(restore)

	plan := plannedWrite{
		dir:   cp.ProjectConfigDir(),
		write: Write{Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "WIDGET_TOKEN", Remove: true}},
	}

	err := executeProjectSecret(plan, ApplyOptions{CheckoutSlug: testCheckoutSlug})
	if err == nil {
		t.Fatal("executeProjectSecret with an unrecordable approval = nil, want the approval error")
	}
}

// TestApplyRestoresTheSecretApprovalWhenTheRemovalFails pins that a project
// secret removal that fails after the approval was extended restores the
// previous approval, so the launcher's own failed change does not need
// re-approval. The removal fails because the secret file is no longer parsable
// once the approval has been extended.
func TestApplyRestoresTheSecretApprovalWhenTheRemovalFails(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "network:\n  profile: none\n")
	secretFile := filepath.Join(cp.ProjectConfigDir(), "env.secret.yaml")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "env.secret.yaml",
		"WIDGET_TOKEN:\n  value: sk\n  host: api.widget.example\n")
	approveProjectLayer(t, cp)
	before, _, err := projecttrust.LoadApproval(testCheckoutSlug)
	require.NoError(t, err)
	// Corrupt the file after the approval was recorded, so the removal fails at
	// execution while the approval step already succeeded.
	if err := os.WriteFile(secretFile, []byte("not: [valid"), 0o600); err != nil {
		t.Fatalf("corrupt secret file: %v", err)
	}

	plan := plannedWrite{
		dir:   cp.ProjectConfigDir(),
		write: Write{Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "WIDGET_TOKEN", Remove: true}},
	}

	err = executeProjectSecret(plan, ApplyOptions{CheckoutSlug: testCheckoutSlug})
	if err == nil {
		t.Fatal("executeProjectSecret over an unparsable secret file = nil, want the removal error")
	}
	after, _, err := projecttrust.LoadApproval(testCheckoutSlug)
	require.NoError(t, err)
	require.Equal(t, before, after, "a failed removal must restore the approval it extended")
}

// TestApplyFailsWhenAProjectSecretRemovalCannotBeRecorded pins that a failing
// project-scope secret removal surfaced through the applier fails the finding:
// executeWrite must carry executeProjectSecret's error, not swallow it.
func TestApplyFailsWhenAProjectSecretRemovalCannotBeRecorded(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "network:\n  profile: none\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "env.secret.yaml",
		"WIDGET_TOKEN:\n  value: sk\n  host: api.widget.example\n")
	approveProjectLayer(t, cp)
	restore := state.OverrideStateWriteForTest(func(string, []byte) error { return errors.New("boom") })
	t.Cleanup(restore)

	ui, _ := interactiveUI()
	finding := Finding{
		Evidence: Evidence{Summary: "WIDGET_TOKEN is committed in the project scope"},
		Writes:   []Write{{Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "WIDGET_TOKEN", Remove: true}}},
	}
	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: cp, Slug: testSlug, CheckoutSlug: testCheckoutSlug, UI: ui, Mode: options.ModeRun,
	})

	if results[0].Outcome != OutcomeFailed || results[0].Err == "" {
		t.Fatalf("result = %+v; want a failed removal carrying the error", results[0])
	}
}

// TestApplyFailsWhenTheSecretFileRefreshCannotBeRecorded pins that the
// home-source refresh after a project secret removal surfaces its failure:
// executeProjectSecret must carry RefreshSecretFileInput's error, not swallow it.
func TestApplyFailsWhenTheSecretFileRefreshCannotBeRecorded(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"network:\n  profile: none\nhome:\n  .config/agents-sandbox/env.secret.yaml:\n    source: env.secret.yaml\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "env.secret.yaml",
		"WIDGET_TOKEN:\n  value: sk\n  host: api.widget.example\n")
	approveProjectLayer(t, cp)

	// The approval extension succeeds; fail the refresh's commit, the second
	// state write, so execution reaches RefreshSecretFileInput.
	writes := 0
	restore := state.OverrideStateWriteForTest(func(string, []byte) error {
		writes++
		if writes > 1 {
			return errors.New("boom")
		}
		return nil
	})
	t.Cleanup(restore)

	plan := plannedWrite{
		dir:   cp.ProjectConfigDir(),
		write: Write{Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "WIDGET_TOKEN", Remove: true}},
	}

	err := executeProjectSecret(plan, ApplyOptions{CheckoutSlug: testCheckoutSlug})
	require.ErrorContains(t, err, "refresh the approval of")
}

// TestPlanWritesKeepsWritesAfterASkippedOrFileModeWrite pins that a declarative
// (text-only) write and a file-mode write are each passed over without stopping
// the planning of the writes that follow them.
func TestPlanWritesKeepsWritesAfterASkippedOrFileModeWrite(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	dir := cp.UserSlugConfigDir(testSlug)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "env.secret.yaml", "TOKEN:\n  value: sk\n  host: api.example\n")
	finding := Finding{
		Evidence: Evidence{Summary: "several writes"},
		Writes: []Write{
			{Scope: ScopePerSlugUser, Text: "declarative only"},
			{Scope: ScopePerSlugUser, FileMode: &FileModeWrite{Mode: 0o600}},
			{Scope: ScopePerSlugUser, Secret: &SecretWrite{EnvVar: "SECOND", Hosts: []string{"api.example"}}},
		},
	}

	plans, refusal := planWrites(finding, ApplyOptions{
		Paths: cp, Slug: testSlug, Mode: options.ModeRun,
		SecretValue: func(SecretWrite) (string, bool) { return "sk-second", true },
	})

	require.Nil(t, refusal)
	require.Len(t, plans, 2, "the file-mode and secret writes are planned past the text-only one")
}

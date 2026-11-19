package analyser

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/projecttrust"
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

// TestApprovalValue renders a config value as the string the trust gate records:
// a string round-trips as itself, every other value as JSON, and an unencodable
// value falls back to its string form rather than an empty record.
func TestApprovalValue(t *testing.T) {
	if got := approvalValue("none"); got != "none" {
		t.Errorf("approvalValue(string) = %q; want the string", got)
	}
	if got := approvalValue([]string{"a.example", "b.example"}); got != `["a.example","b.example"]` {
		t.Errorf("approvalValue(list) = %q; want its JSON", got)
	}
	if got := approvalValue(math.NaN()); got != "NaN" {
		t.Errorf("approvalValue(NaN) = %q; want the fallback string form", got)
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

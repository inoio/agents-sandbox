package analyser

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
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

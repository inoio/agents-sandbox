package firstrun

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestPromptLearnByUsingSetsLearn pins the UC1 choice: "Learn by using it"
// starts a learning session and records the choice without seeding anything.
func TestPromptLearnByUsingSetsLearn(t *testing.T) {
	var gotPrompt string
	var gotDefault string
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(prompt string, _ []termio.Choice, defaultKey string) (string, error) {
		gotPrompt = prompt
		gotDefault = defaultKey
		return string(ChoiceLearnByUsing), nil
	}

	out, offered := Prompt(ui, Facts{Agent: "pi", Slug: "widget-abc"})
	if !offered {
		t.Fatal("an undecided checkout with no managed config must offer the menu")
	}
	if !out.Learn {
		t.Error("Learn by using it must start a learning session")
	}
	if out.Choice != ChoiceLearnByUsing {
		t.Errorf("Choice = %q, want %q", out.Choice, ChoiceLearnByUsing)
	}
	if gotPrompt == "" {
		t.Error("Select must be given the menu question")
	}
	if gotDefault != string(ChoiceLearnByUsing) {
		t.Errorf("default key = %q, want %q with no host config", gotDefault, ChoiceLearnByUsing)
	}
	if !slices.Contains(ui.InfoCalls, firstRunConsent) {
		t.Errorf("Info calls = %v, want the consent sentence shown", ui.InfoCalls)
	}
}

// TestPromptConsentSentenceMatchesHub pins the one consent sentence against the
// exact words the learn-mode hub specifies, so a reworded constant cannot drift
// from the documented contract.
func TestPromptConsentSentenceMatchesHub(t *testing.T) {
	const hubConsent = "Let agents-sandbox learn from this session: set up your providers here, " +
		"use the agent normally, and when you exit it offers to save what it needed."
	if firstRunConsent != hubConsent {
		t.Errorf("firstRunConsent = %q, want the hub's sentence %q", firstRunConsent, hubConsent)
	}
}

// TestPromptLearningChoiceDoesNotConfirm pins that only the manual choice asks
// the provisioning warning: a learning choice must never call Confirm.
func TestPromptLearningChoiceDoesNotConfirm(t *testing.T) {
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return string(ChoiceLearnFromHost), nil
	}

	out, offered := Prompt(ui, Facts{
		Agent:     "pi",
		Slug:      "widget-abc",
		HostFiles: []string{"/home/dev/.pi/agent/settings.json"},
	})
	if !offered {
		t.Fatal("the menu must be offered")
	}
	if len(ui.ConfirmCalls) != 0 {
		t.Errorf("Confirm calls = %v, want none for a learning choice", ui.ConfirmCalls)
	}
	if out.Choice != ChoiceLearnFromHost || !out.Learn {
		t.Errorf("outcome = %+v, want a seeded learning session", out)
	}
}

// TestPromptOfferedFalseWhenNotInteractive pins invariant 6: a non-interactive
// run offers nothing, never calls Select, warns, and returns the zero Outcome.
func TestPromptOfferedFalseWhenNotInteractive(t *testing.T) {
	selectCalled := false
	ui := &termiotest.Mock{}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		selectCalled = true
		return string(ChoiceLearnByUsing), nil
	}

	out, offered := Prompt(ui, Facts{Agent: "pi", Slug: "widget-abc"})
	if offered {
		t.Fatal("a non-interactive run must not offer the menu")
	}
	if out != (Outcome{}) {
		t.Errorf("outcome = %+v, want the zero Outcome", out)
	}
	if selectCalled {
		t.Error("Select must not be called on a non-interactive run")
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("a non-interactive run must warn and continue")
	}
}

// TestPromptOfferedFalseWhenManaged pins that a checkout with managed config
// never reaches Select.
func TestPromptOfferedFalseWhenManaged(t *testing.T) {
	selectCalled := false
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		selectCalled = true
		return string(ChoiceLearnByUsing), nil
	}

	if _, offered := Prompt(ui, Facts{Agent: "pi", Slug: "widget-abc", ManagedConfig: true}); offered {
		t.Fatal("managed config must suppress the menu")
	}
	if selectCalled {
		t.Error("Select must not be called when the menu is not offered")
	}
}

// TestPromptTwoChoicesWithoutHostConfig pins Design decision 8: with no host
// config the menu offers two choices and no "Learn from my host setup".
func TestPromptTwoChoicesWithoutHostConfig(t *testing.T) {
	var got []termio.Choice
	var gotDefault string
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(_ string, choices []termio.Choice, defaultKey string) (string, error) {
		got = choices
		gotDefault = defaultKey
		return string(ChoiceLearnByUsing), nil
	}

	if _, offered := Prompt(ui, Facts{Agent: "pi", Slug: "widget-abc"}); !offered {
		t.Fatal("the menu must be offered")
	}
	if len(got) != 2 {
		t.Fatalf("menu = %d choices, want 2: %v", len(got), got)
	}
	for _, choice := range got {
		if choice.Key == string(ChoiceLearnFromHost) {
			t.Error("learn-from-host must be absent with no host config")
		}
	}
	if gotDefault != string(ChoiceLearnByUsing) {
		t.Errorf("default key = %q, want %q with no host config", gotDefault, ChoiceLearnByUsing)
	}
}

// TestPromptLearnFromHostRecommendedWithHostConfig pins that a host config adds
// the third choice and makes it the recommended default.
func TestPromptLearnFromHostRecommendedWithHostConfig(t *testing.T) {
	var got []termio.Choice
	var gotDefault string
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(_ string, choices []termio.Choice, defaultKey string) (string, error) {
		got = choices
		gotDefault = defaultKey
		return string(ChoiceLearnFromHost), nil
	}

	out, offered := Prompt(ui, Facts{
		Agent:     "pi",
		Slug:      "widget-abc",
		HostFiles: []string{"/home/dev/.pi/agent/settings.json"},
	})
	if !offered {
		t.Fatal("the menu must be offered")
	}
	var found bool
	for _, choice := range got {
		if choice.Key == string(ChoiceLearnFromHost) {
			found = true
		}
	}
	if !found {
		t.Errorf("learn-from-host must be offered when the host has config: %v", got)
	}
	if gotDefault != string(ChoiceLearnFromHost) {
		t.Errorf("default key = %q, want %q when the host has config", gotDefault, ChoiceLearnFromHost)
	}
	if !out.Learn || out.Choice != ChoiceLearnFromHost {
		t.Errorf("outcome = %+v, want a seeded learning session", out)
	}
}

// TestPromptManualWarnsBeforeProvisioning pins the manual path's one consent
// point: choosing "configure it myself" asks the provisioning warning before
// returning, and a yes answer turns on the unsafe opt-in.
func TestPromptManualWarnsBeforeProvisioning(t *testing.T) {
	ui := &termiotest.Mock{IsInteractiveResult: true, ConfirmResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return string(ChoiceManual), nil
	}

	out, offered := Prompt(ui, Facts{Agent: "pi", Slug: "widget-abc"})
	if !offered {
		t.Fatal("the menu must be offered")
	}
	if len(ui.ConfirmCalls) != 1 || ui.ConfirmCalls[0] != manualProvisionWarning {
		t.Fatalf(
			"Confirm calls = %v, want exactly the provisioning warning %q",
			ui.ConfirmCalls,
			manualProvisionWarning,
		)
	}
	if out.Choice != ChoiceManual {
		t.Errorf("Choice = %q, want %q", out.Choice, ChoiceManual)
	}
	if !out.ProvisionHostConfig {
		t.Error("a confirmed provisioning warning must set Outcome.ProvisionHostConfig")
	}
	if out.Learn {
		t.Error("the manual choice must not start a learning session")
	}
}

// TestPromptManualDeclinedLeavesProvisioningOff pins that declining the warning
// keeps the manual choice but leaves the unsafe opt-in off.
func TestPromptManualDeclinedLeavesProvisioningOff(t *testing.T) {
	ui := &termiotest.Mock{IsInteractiveResult: true, ConfirmResult: false}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return string(ChoiceManual), nil
	}

	out, offered := Prompt(ui, Facts{Agent: "pi", Slug: "widget-abc"})
	if !offered {
		t.Fatal("the menu must be offered")
	}
	if len(ui.ConfirmCalls) != 1 {
		t.Fatalf("Confirm calls = %d, want the warning to be asked once", len(ui.ConfirmCalls))
	}
	if out.Choice != ChoiceManual {
		t.Errorf("Choice = %q, want %q after declining", out.Choice, ChoiceManual)
	}
	if out.ProvisionHostConfig {
		t.Error("a declined provisioning warning must leave Outcome.ProvisionHostConfig false")
	}
	if out.Learn {
		t.Error("the manual choice must not start a learning session")
	}
}

// TestPromptOfferedFalseWhenUINil pins the nil-UI guard: a caller that offers
// the menu with no UI gets no prompt and the zero outcome rather than a panic.
func TestPromptOfferedFalseWhenUINil(t *testing.T) {
	out, offered := Prompt(nil, Facts{Agent: "pi", Slug: "widget-abc"})
	if offered {
		t.Fatal("a nil UI must not offer the menu")
	}
	if out != (Outcome{}) {
		t.Errorf("outcome = %+v, want the zero Outcome", out)
	}
}

// TestPromptWarnsAndSkipsWhenSelectFails pins the select-failure path: a UI that
// cannot present the menu is warned about and yields the zero outcome instead of
// an error, so a failed prompt never blocks the run.
func TestPromptWarnsAndSkipsWhenSelectFails(t *testing.T) {
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return "", errors.New("no tty")
	}

	out, offered := Prompt(ui, Facts{Agent: "pi", Slug: "widget-abc"})
	if offered {
		t.Fatal("a failed Select must not offer an outcome")
	}
	if out != (Outcome{}) {
		t.Errorf("outcome = %+v, want the zero Outcome", out)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(w string) bool { return strings.Contains(w, "no tty") }) {
		t.Errorf("warnings = %v, want the Select error surfaced", ui.WarnCalls)
	}
}

// TestPromptWarnsAndSkipsWhenConfirmFails pins the manual path's confirm
// failure: a UI that cannot ask the warning is warned about and yields the zero
// outcome, so an unconfirmed provisioning opt-in is never applied.
func TestPromptWarnsAndSkipsWhenConfirmFails(t *testing.T) {
	ui := &termiotest.Mock{IsInteractiveResult: true, ConfirmErr: errors.New("no tty")}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return string(ChoiceManual), nil
	}

	out, offered := Prompt(ui, Facts{Agent: "pi", Slug: "widget-abc"})
	if offered {
		t.Fatal("a failed Confirm must not offer an outcome")
	}
	if out != (Outcome{}) {
		t.Errorf("outcome = %+v, want the zero Outcome", out)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(w string) bool { return strings.Contains(w, "no tty") }) {
		t.Errorf("warnings = %v, want the Confirm error surfaced", ui.WarnCalls)
	}
}

// TestOutcomeForUnexpectedKeyFallsBackToLearnByUsing pins the defensive
// fallback: Prompt must never drop a first run, so a key outside the menu's
// listed set maps to the unseeded learning session. The Select seam can return
// any key, so the fallback is reachable and is exercised here directly.
func TestOutcomeForUnexpectedKeyFallsBackToLearnByUsing(t *testing.T) {
	out := outcomeFor("not-a-listed-key", "hostconfig-abc123", false)

	if out.Choice != ChoiceLearnByUsing || !out.Learn {
		t.Errorf("outcome = %+v, want the unseeded learning session", out)
	}
	if out.sourceHash != "hostconfig-abc123" {
		t.Errorf("sourceHash = %q, want it carried through", out.sourceHash)
	}
}

// TestOutcomeApplyRecordsLearned pins that Apply sets opts.Learn and records the
// learned decision against the host-config source hash.
func TestOutcomeApplyRecordsLearned(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	opts := options.RunOptions{}
	outcome := Outcome{
		Choice:              ChoiceLearnByUsing,
		Learn:               true,
		ProvisionHostConfig: false,
		sourceHash:          "hostconfig-abc123",
	}
	if err := outcome.Apply(&opts, "widget-abc", "pi"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !opts.Learn {
		t.Error("Apply must set opts.Learn for a learning choice")
	}

	got, ok, err := state.ReadFirstRun("widget-abc", "pi")
	if err != nil {
		t.Fatalf("ReadFirstRun: %v", err)
	}
	if !ok {
		t.Fatal("Apply must record the first-run decision")
	}
	if got.Choice != state.FirstRunLearned {
		t.Errorf("recorded choice = %q, want %q", got.Choice, state.FirstRunLearned)
	}
	if got.SourceHash != "hostconfig-abc123" {
		t.Errorf("recorded source hash = %q, want %q", got.SourceHash, "hostconfig-abc123")
	}
	if got.At.IsZero() {
		t.Error("Apply must let RecordFirstRun stamp At")
	}
}

// TestOutcomeApplyRecordsManual pins that Apply carries the manual choice's
// provisioning opt-in onto opts and records the manual decision.
func TestOutcomeApplyRecordsManual(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	opts := options.RunOptions{}
	outcome := Outcome{
		Choice:              ChoiceManual,
		Learn:               false,
		ProvisionHostConfig: true,
		sourceHash:          "hostconfig-abc123",
	}
	if err := outcome.Apply(&opts, "widget-abc", "pi"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if opts.Learn {
		t.Error("Apply must not set opts.Learn for the manual choice")
	}
	if opts.ProvisionHostConfig == nil || !*opts.ProvisionHostConfig {
		t.Errorf("ProvisionHostConfig = %v, want a pointer to true from the manual choice", opts.ProvisionHostConfig)
	}

	got, ok, err := state.ReadFirstRun("widget-abc", "pi")
	if err != nil {
		t.Fatalf("ReadFirstRun: %v", err)
	}
	if !ok {
		t.Fatal("Apply must record the first-run decision")
	}
	if got.Choice != state.FirstRunManual {
		t.Errorf("recorded choice = %q, want %q", got.Choice, state.FirstRunManual)
	}
	if got.SourceHash != "hostconfig-abc123" {
		t.Errorf("recorded source hash = %q, want %q", got.SourceHash, "hostconfig-abc123")
	}
}

// TestOutcomeApplyLearningLeavesProvisionHostConfigUntouched pins that a
// learning choice applies nothing to the config-resolved provisioning setting.
func TestOutcomeApplyLearningLeavesProvisionHostConfigUntouched(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	configured := true
	opts := options.RunOptions{ProvisionHostConfig: &configured}
	outcome := Outcome{Choice: ChoiceLearnByUsing, Learn: true, ProvisionHostConfig: false}
	if err := outcome.Apply(&opts, "widget-abc", "pi"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if opts.ProvisionHostConfig != &configured {
		t.Error("a learning choice must leave the config-resolved ProvisionHostConfig untouched")
	}
}

// TestPromptOffersTheHostSeedForInstructionFilesAlone pins that instruction
// files alone are a host setup: a claude-code host whose settings.json is
// withheld still gets "Learn from my host setup", recommended.
func TestPromptOffersTheHostSeedForInstructionFilesAlone(t *testing.T) {
	var gotKeys []string
	var gotDefault string
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(_ string, choices []termio.Choice, defaultKey string) (string, error) {
		for _, choice := range choices {
			gotKeys = append(gotKeys, choice.Key)
		}
		gotDefault = defaultKey
		return defaultKey, nil
	}

	_, offered := Prompt(ui, Facts{
		Agent:            "claude-code",
		Slug:             "widget-abc",
		InstructionFiles: []string{"/home/dev/.claude/CLAUDE.md"},
	})

	if !offered {
		t.Fatal("the menu must be offered")
	}
	if !slices.Contains(gotKeys, string(ChoiceLearnFromHost)) {
		t.Errorf("choices = %v, want the host seed offered", gotKeys)
	}
	if gotDefault != string(ChoiceLearnFromHost) {
		t.Errorf("default = %q, want the host seed recommended", gotDefault)
	}
}

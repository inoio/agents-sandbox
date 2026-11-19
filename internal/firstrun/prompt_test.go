package firstrun

import (
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

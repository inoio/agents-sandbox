package journeys

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// recordingTB records what the runner reports. Fatalf only records: the runner
// must return after it, as it would after a real t.Fatalf.
type recordingTB struct {
	logs   []string
	errors []string
	fatals []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
}

// fixedClock advances one second per call, so step durations are predictable.
func fixedClock() func() time.Time {
	current := time.Unix(0, 0)
	return func() time.Time {
		current = current.Add(time.Second)
		return current
	}
}

func recordStep(name string, ran *[]string, err error) Step {
	return Step{Name: name, Run: func(*Context) error {
		*ran = append(*ran, name)
		return err
	}}
}

func TestRunJourneyStopsAtAFailedAction(t *testing.T) {
	var ran []string
	journey := Journey{Name: "j", Steps: []Step{
		Given(recordStep("a fresh home", &ran, nil)),
		When(recordStep("the harness starts", &ran, errors.New("no TUI"))),
		Then(AsCheck(recordStep("the reply is shown", &ran, nil))),
	}}
	tb := &recordingTB{}

	runJourney(tb, &Context{}, journey, fixedClock())

	if got := strings.Join(ran, ","); got != "a fresh home,the harness starts" {
		t.Errorf("ran %q, want the steps up to the failed action", got)
	}
	if len(tb.fatals) != 1 || !strings.Contains(tb.fatals[0], "When the harness starts") ||
		!strings.Contains(tb.fatals[0], "no TUI") {
		t.Errorf("fatals = %q, want one naming the step in Gherkin form and its error", tb.fatals)
	}
}

func TestRunJourneyRecordsAFailedCheckAndContinues(t *testing.T) {
	var ran []string
	journey := Journey{Name: "j", Steps: []Step{
		Then(AsCheck(recordStep("the scope was written", &ran, errors.New("missing")))),
		And(AsCheck(recordStep("no scaffolding appeared", &ran, nil))),
	}}
	tb := &recordingTB{}

	runJourney(tb, &Context{}, journey, fixedClock())

	if len(ran) != 2 {
		t.Errorf("ran %q, want both checks", ran)
	}
	if len(tb.fatals) != 0 || len(tb.errors) != 1 || !strings.Contains(tb.errors[0], "Then the scope was written") {
		t.Errorf("errors = %q, fatals = %q; want one recorded check failure", tb.errors, tb.fatals)
	}
}

func TestRunJourneyLogsTheScenarioAndEachStepsDuration(t *testing.T) {
	var ran []string
	journey := Journey{Name: "core-session", Steps: []Step{
		Given(recordStep("a fresh home", &ran, nil)),
		When(recordStep("the harness starts", &ran, nil)),
	}}
	tb := &recordingTB{}

	runJourney(tb, &Context{Agent: "opencode2"}, journey, fixedClock())

	logs := strings.Join(tb.logs, "\n")
	scenario := "Scenario: core-session (opencode2)\n  Given a fresh home\n  When the harness starts"
	if !strings.Contains(logs, scenario) {
		t.Errorf("logs = %q, want the scenario %q before the run", logs, scenario)
	}
	if !strings.Contains(logs, "Given a fresh home (1s)") || !strings.Contains(logs, "When the harness starts (1s)") {
		t.Errorf("logs = %q, want each step with its duration", logs)
	}
}

func TestRunJourneyReportsNoSessionOutputBeforeAHarnessStarted(t *testing.T) {
	var ran []string
	tb := &recordingTB{}

	runJourney(tb, &Context{}, Journey{Name: "j", Steps: []Step{Given(recordStep("x", &ran, errors.New("boom")))}},
		fixedClock())

	if len(tb.fatals) != 1 || strings.Contains(tb.fatals[0], "harness output") {
		t.Errorf("fatals = %q, want the failure without a harness output section", tb.fatals)
	}
}

func TestJourneySpecAppliesTheJourneysAndTheAdaptersOverrides(t *testing.T) {
	smoke := driver.Spec{Network: "none", ReadyMarker: "smoke marker"}

	unchanged := journeySpec(smoke, Journey{}, Harness{})
	if unchanged.Network != "none" || unchanged.ReadyMarker != "smoke marker" {
		t.Errorf("spec without overrides = %+v, want the smoke's", unchanged)
	}
	overridden := journeySpec(smoke, Journey{Network: "host"}, Harness{Drive: Driving{ReadyMarker: "status line"}})
	if overridden.Network != "host" || overridden.ReadyMarker != "status line" {
		t.Errorf("spec with overrides = %+v, want the journey's network and the adapter's marker", overridden)
	}
}

func TestSelectedProjectAgentsFollowTheSelection(t *testing.T) {
	journeys := []Journey{
		{Name: "a", Harnesses: []string{"opencode2", "codex"}},
		{Name: "b", Harnesses: []string{"opencode2", "pi"}},
	}
	names := func(pairs []driver.ProjectAgent) []string {
		var agents []string
		for _, pair := range pairs {
			if pair.Project != pair.Agent || pair.Docker {
				t.Errorf("pair = %+v, want the harness's own project without docker", pair)
			}
			agents = append(agents, pair.Agent)
		}
		return agents
	}
	for _, tc := range []struct {
		journey, harness string
		want             []string
	}{
		{"", "", []string{"opencode2", "codex", "pi"}},
		{"b", "", []string{"opencode2", "pi"}},
		{"", "pi", []string{"pi"}},
		{"a", "pi", nil},
	} {
		if got := names(selectedProjectAgents(journeys, tc.journey, tc.harness)); !slices.Equal(got, tc.want) {
			t.Errorf("selection (%q, %q) = %v, want %v", tc.journey, tc.harness, got, tc.want)
		}
	}
}

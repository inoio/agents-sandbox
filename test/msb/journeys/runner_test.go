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
	skips  []string
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

func (r *recordingTB) Skipf(format string, args ...any) {
	r.skips = append(r.skips, fmt.Sprintf(format, args...))
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

func TestRunHarnessCellSchedulesParallelismAndReleasesItsSlot(t *testing.T) {
	for _, serial := range []bool{false, true} {
		t.Run(fmt.Sprintf("serial=%t", serial), func(t *testing.T) {
			parallel := false
			ran := false
			slots := make(chan struct{}, 1)
			runHarnessCell(Journey{SerialHarnesses: serial}, func() { parallel = true }, slots, func() {
				ran = true
				if len(slots) != 1 || parallel == serial {
					t.Errorf("occupied slots %d, parallel %t, serial %t", len(slots), parallel, serial)
				}
			})
			if !ran || len(slots) != 0 {
				t.Fatalf("ran %t, slots left %d; want execution and a released slot", ran, len(slots))
			}
		})
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

	if len(tb.fatals) != 1 || strings.Contains(tb.fatals[0], "session") {
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

func TestRunJourneySkipsTheRestOnASkipStep(t *testing.T) {
	var ran []string
	journey := Journey{Name: "opt-in", Steps: []Step{
		Given(Step{Name: "opted in", Run: func(*Context) error { return skipJourneyError{reason: "not opted in"} }}),
		When(recordStep("the harness starts", &ran, nil)),
	}}
	tb := &recordingTB{}

	runJourney(tb, &Context{}, journey, fixedClock())

	if len(ran) != 0 || len(tb.skips) != 1 || len(tb.fatals) != 0 || !strings.Contains(tb.skips[0], "not opted in") {
		t.Errorf(
			"ran %q, skips %q, fatals %q; want one skip naming the reason and nothing run",
			ran,
			tb.skips,
			tb.fatals,
		)
	}
}

func TestStepRequireOptInSkipsUnlessTheSwitchIsSet(t *testing.T) {
	const env = "AGENTS_SANDBOX_JOURNEYS_TEST_OPT_IN"
	t.Setenv(env, "")
	var skip skipJourneyError
	if err := StepRequireOptIn(
		env,
		"a spike",
	).Run(&Context{}); !errors.As(err, &skip) ||
		!strings.Contains(skip.reason, env) {
		t.Errorf("without the switch: err = %v, want a skip naming %s", err, env)
	}
	t.Setenv(env, "1")
	if err := StepRequireOptIn(env, "a spike").Run(&Context{}); err != nil {
		t.Errorf("with the switch: err = %v, want nil", err)
	}
}

func TestRunJourneyReportsEveryLauncherSessionsOutput(t *testing.T) {
	earlier := sessionPrinting(t, "the after-run failed")
	active := sessionPrinting(t, "replay output")
	tb := &recordingTB{}
	check := AsCheck(Step{Name: "x", Run: func(*Context) error { return errors.New("boom") }})

	runJourney(tb, &Context{Session: active, Clients: []*driver.Session{earlier, active}},
		Journey{Name: "j", Steps: []Step{Then(check)}}, fixedClock())

	if len(tb.errors) != 1 {
		t.Fatalf("errors = %q, want one", tb.errors)
	}
	report := tb.errors[0]
	for _, want := range []string{
		"launcher session 1 of 2 output:", "the after-run failed",
		"launcher session 2 of 2 (active) output:", "replay output",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report = %q, want %q", report, want)
		}
	}
}

func TestSessionTailReportsAnActiveSessionOutsideTheClients(t *testing.T) {
	shell := sessionPrinting(t, "shell output")

	report := sessionTail(&Context{Session: shell})

	if !strings.Contains(report, "launcher session 1 of 1 (active) output:") ||
		!strings.Contains(report, "shell output") {
		t.Errorf("report = %q, want the active session", report)
	}
}

// sessionPrinting starts a pty session that prints text and returns once its
// output holds it. The process exiting does not mean the pty reader has caught
// up, so the wait is on the output, not on the exit.
func sessionPrinting(t *testing.T, text string) *driver.Session {
	t.Helper()
	session := driver.MustStartUnderPTY(t, t.TempDir(), nil, "sh", "-c", "printf '"+text+"'")
	printed := func() (bool, error) { return strings.Contains(string(session.Output()), text), nil }
	if err := pollUntil(10*time.Second, 10*time.Millisecond, printed, func() error {
		return fmt.Errorf("the session never printed %q", text)
	}); err != nil {
		t.Fatal(err)
	}
	return session
}

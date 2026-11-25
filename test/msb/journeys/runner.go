package journeys

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// journeyTB is the part of a *testing.T the runner reports through.
type journeyTB interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
}

// skipJourneyError is the error a step returns to skip the rest of its journey,
// for an opt-in journey that was not opted into.
type skipJourneyError struct {
	reason string
}

func (s skipJourneyError) Error() string { return "skipped: " + s.reason }

// runJourney logs the journey's scenario, then runs its steps in order on ctx,
// logging each step with its duration. A failed Action ends the journey through
// tb.Fatalf; a failed Check is recorded through tb.Errorf and the journey goes
// on; a step returning skipJourneyError skips the rest through tb.Skipf. now is
// the clock the durations are taken from.
func runJourney(tb journeyTB, ctx *Context, journey Journey, now func() time.Time) {
	tb.Helper()
	tb.Logf("%s", scenarioText(journey, ctx.Agent))
	for _, step := range journey.Steps {
		started := now()
		err := step.Run(ctx)
		duration := now().Sub(started).Round(time.Millisecond)
		tb.Logf("%s (%s)", step.Text(), duration)
		if err == nil {
			continue
		}
		if skip, ok := errors.AsType[skipJourneyError](err); ok {
			tb.Skipf("journey %q: %s", journey.Name, skip.reason)
			return
		}
		if step.Kind == Check {
			tb.Errorf("journey %q: %s: %v%s", journey.Name, step.Text(), err, sessionTail(ctx))
			continue
		}
		tb.Fatalf("journey %q: %s: %v%s", journey.Name, step.Text(), err, sessionTail(ctx))
		return
	}
}

// scenarioText renders the journey as a Gherkin scenario for agentName.
func scenarioText(journey Journey, agentName string) string {
	var text strings.Builder
	text.WriteString("Scenario: " + journey.Name + " (" + agentName + ")")
	for _, step := range journey.Steps {
		text.WriteString("\n  " + step.Text())
	}
	return text.String()
}

// sessionTail is the recent output of every launcher session the journey
// started, for a failure report, or "" before a step started one. A check often
// fails on what an earlier session did (a preliminary run's after-run warning),
// so each client is reported, the active one marked.
func sessionTail(ctx *Context) string {
	sessions := ctx.Clients
	if ctx.Session != nil && !slices.Contains(sessions, ctx.Session) {
		sessions = append(slices.Clone(sessions), ctx.Session)
	}
	var report strings.Builder
	for index, session := range sessions {
		label := fmt.Sprintf("launcher session %d of %d", index+1, len(sessions))
		if session == ctx.Session {
			label += " (active)"
		}
		report.WriteString("\n" + label + " output:\n" + session.OutputTail())
	}
	return report.String()
}

// journeySpec is the startup smoke's spec for a harness as a journey runs it:
// the journey's network profile and the adapter's readiness marker override
// the smoke's when set.
func journeySpec(spec driver.Spec, journey Journey, adapter Harness) driver.Spec {
	if journey.Network != "" {
		spec.Network = journey.Network
	}
	if adapter.Drive.ReadyMarker != "" {
		spec.ReadyMarker = adapter.Drive.ReadyMarker
	}
	return spec
}

// selectedProjectAgents are the runner images the selected journeys drive: each
// harness of each journey named journey (all when empty), narrowed to harness
// when set, in table order and without duplicates. The suite prebuilds only
// these, never the startup smoke's whole set.
func selectedProjectAgents(journeys []Journey, journey, harness string) []driver.ProjectAgent {
	var pairs []driver.ProjectAgent
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			pairs = append(pairs, driver.ProjectAgent{Project: name, Agent: name, Docker: false})
		}
	}
	for _, candidate := range journeys {
		if journey != "" && candidate.Name != journey {
			continue
		}
		for _, name := range candidate.Harnesses {
			if harness == "" || name == harness {
				add(name)
			}
		}
	}
	return pairs
}

// StepRequireOptIn skips the journey unless env is set: an opt-in journey,
// such as the proxy spike, runs only when asked for.
func StepRequireOptIn(env, reason string) Step {
	return Step{Name: "the run opts in with " + env, Run: func(*Context) error {
		//testenv:allow an opt-in journey reads its own switch
		if os.Getenv(env) == "" {
			return skipJourneyError{reason: "set " + env + "=1 to run it: " + reason}
		}
		return nil
	}}
}

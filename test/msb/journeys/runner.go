package journeys

import (
	"strings"
	"time"
)

// journeyTB is the part of a *testing.T the runner reports through.
type journeyTB interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// runJourney logs the journey's scenario, then runs its steps in order on ctx,
// logging each step with its duration. A failed Action ends the journey through
// tb.Fatalf; a failed Check is recorded through tb.Errorf and the journey goes
// on. now is the clock the durations are taken from.
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
		if step.Kind == Check {
			tb.Errorf("journey %q: %s: %v", journey.Name, step.Text(), err)
			continue
		}
		tb.Fatalf("journey %q: %s: %v", journey.Name, step.Text(), err)
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

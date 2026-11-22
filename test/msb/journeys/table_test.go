package journeys

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// TestEveryRegisteredAgentIsCoveredOrExempted mirrors the startup-smoke
// completeness contract for the journeys table: every registered harness is
// either driven by a journey or carries an exemption with a recorded reason, so
// a newly registered agent fails the default suite until it is covered or
// explicitly exempted. A harness that gains a journey must drop its stale
// exemption.
func TestEveryRegisteredAgentIsCoveredOrExempted(t *testing.T) {
	covered := make(map[string]bool, len(Journeys))
	for _, journey := range Journeys {
		if _, ok := agent.Lookup(journey.Agent); !ok {
			t.Errorf("journey %q names unknown agent %q", journey.Name, journey.Agent)
			continue
		}
		if covered[journey.Agent] {
			t.Errorf("agent %q is driven by more than one journey", journey.Agent)
		}
		covered[journey.Agent] = true
	}

	names := agent.Names()
	if len(names) == 0 {
		t.Fatal("no agents registered")
	}
	for _, name := range names {
		if covered[name] {
			if reason, ok := Exemptions[name]; ok {
				t.Errorf("agent %q is covered by a journey but still carries an exemption %q", name, reason)
			}
			continue
		}
		reason, ok := Exemptions[name]
		if !ok {
			t.Errorf("registered agent %q is neither covered by a journey nor exempted", name)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("agent %q has an exemption with no recorded reason", name)
		}
	}

	for name := range Exemptions {
		if _, ok := agent.Lookup(name); !ok {
			t.Errorf("exemption %q matches no registered agent", name)
		}
	}
}

// TestEveryJourneyStepIsNamedAndRunnable pins the runner's contract: a step
// failure names the step, so every step carries a non-empty Name and a Run, and
// a journey is non-empty and names its agent.
func TestEveryJourneyStepIsNamedAndRunnable(t *testing.T) {
	for _, journey := range Journeys {
		if strings.TrimSpace(journey.Name) == "" {
			t.Errorf("a journey has no name")
		}
		if strings.TrimSpace(journey.Agent) == "" {
			t.Errorf("journey %q names no agent", journey.Name)
		}
		if len(journey.Steps) == 0 {
			t.Errorf("journey %q has no steps", journey.Name)
		}
		for index, step := range journey.Steps {
			if strings.TrimSpace(step.Name) == "" {
				t.Errorf("journey %q step %d has no name", journey.Name, index)
			}
			if step.Run == nil {
				t.Errorf("journey %q step %q has no Run", journey.Name, step.Name)
			}
		}
	}
}

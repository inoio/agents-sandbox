package journeys

import (
	"fmt"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestEveryRegisteredAgentIsCoveredOrExempted mirrors the startup-smoke
// completeness contract for the journeys table: every registered harness is
// either driven by a journey or carries an exemption with a recorded reason, so
// a newly registered agent fails the default suite until it is covered or
// explicitly exempted. A harness that gains a journey must drop its stale
// exemption. An agent may be driven by more than one journey (v1 drives
// opencode2 with UC1 and UC4), so a second journey is not itself a problem.
func TestEveryRegisteredAgentIsCoveredOrExempted(t *testing.T) {
	names := agent.Names()
	if len(names) == 0 {
		t.Fatal("no agents registered")
	}
	for _, problem := range journeyCoverageProblems(Journeys, Exemptions, names) {
		t.Error(problem)
	}
}

// TestJourneyCoverageAllowsMultipleJourneysPerAgent pins the completeness
// contract's shape: an agent driven by more than one journey is covered, not a
// problem. Only an unknown agent, an agent neither covered nor exempted, a blank
// exemption reason, or an exemption naming no registered agent is reported.
func TestJourneyCoverageAllowsMultipleJourneysPerAgent(t *testing.T) {
	journeys := []Journey{
		{Name: "one", Agent: "opencode2"},
		{Name: "two", Agent: "opencode2"},
	}
	problems := journeyCoverageProblems(journeys, nil, []string{"opencode2"})
	if len(problems) != 0 {
		t.Errorf("coverage problems = %v, want none for one agent with two journeys", problems)
	}
}

// journeyCoverageProblems returns the table-completeness problems for journeys
// against exemptions over the registered agent names: a journey naming an
// unknown agent; a registered agent covered by no journey and carrying no
// exemption; an exemption with a blank reason; and an exemption naming no
// registered agent. It takes the table as arguments so the
// multiple-journeys-per-agent contract is testable without the real table, but
// it is not pure: it validates journey agents against the live agent.Lookup
// registry.
func journeyCoverageProblems(journeys []Journey, exemptions map[string]string, registered []string) []string {
	covered := make(map[string]bool, len(journeys))
	var problems []string
	for _, journey := range journeys {
		if _, ok := agent.Lookup(journey.Agent); !ok {
			problems = append(problems, fmt.Sprintf("journey %q names unknown agent %q", journey.Name, journey.Agent))
			continue
		}
		covered[journey.Agent] = true
	}

	for _, name := range registered {
		if covered[name] {
			if reason, ok := exemptions[name]; ok {
				problems = append(
					problems,
					fmt.Sprintf("agent %q is covered by a journey but still carries an exemption %q", name, reason),
				)
			}
			continue
		}
		reason, ok := exemptions[name]
		if !ok {
			problems = append(
				problems,
				fmt.Sprintf("registered agent %q is neither covered by a journey nor exempted", name),
			)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			problems = append(problems, fmt.Sprintf("agent %q has an exemption with no recorded reason", name))
		}
	}

	for name := range exemptions {
		if _, ok := agent.Lookup(name); !ok {
			problems = append(problems, fmt.Sprintf("exemption %q matches no registered agent", name))
		}
	}
	return problems
}

// TestAttributionJourneyRegistered pins that the attribution journey is in the
// table, so the suite drives a `run --learn` session against the stub and
// asserts the learned scope. Without this entry the journey file would compile
// but never run.
func TestAttributionJourneyRegistered(t *testing.T) {
	for _, journey := range Journeys {
		if journey.Name == attributionJourney.Name {
			if journey.Agent != opencode2AgentName {
				t.Errorf("attribution journey agent = %q, want %q", journey.Agent, opencode2AgentName)
			}
			return
		}
	}
	t.Errorf("the attribution journey %q is not registered in Journeys", attributionJourney.Name)
}

// TestAttributionJourneyDrivesABroadBaseline pins the journey's network override:
// it must run under a profile that admits the stub, because under the default
// none the analyser records no used destination and writes no network posture,
// leaving StepAssertNetworkNotWidened vacuous (see attributionJourney).
func TestAttributionJourneyDrivesABroadBaseline(t *testing.T) {
	if attributionJourney.Network != driver.PublicNetwork {
		t.Errorf("attribution journey network = %q, want %q", attributionJourney.Network, driver.PublicNetwork)
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

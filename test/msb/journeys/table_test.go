package journeys

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestEveryJourneyHarnessPairIsCoveredOrExempted mirrors the startup-smoke
// completeness contract per journey: every registered harness is driven by the
// journey or carries an exemption for it with a recorded reason, so a newly
// registered agent fails the default suite until it is placed.
func TestEveryJourneyHarnessPairIsCoveredOrExempted(t *testing.T) {
	names := agent.Names()
	if len(names) == 0 {
		t.Fatal("no agents registered")
	}
	for _, problem := range journeyCoverageProblems(Journeys, Exemptions, names) {
		t.Error(problem)
	}
}

func TestJourneyCoverageProblems(t *testing.T) {
	journeys := []Journey{{Name: "core", Harnesses: []string{"opencode2"}}}
	cases := []struct {
		name       string
		journeys   []Journey
		exemptions map[string]map[string]string
		want       []string
	}{
		{
			name:       "covered and exempted pairs",
			journeys:   journeys,
			exemptions: map[string]map[string]string{"core": {"codex": "later"}},
			want:       nil,
		},
		{
			name:     "a pair neither covered nor exempted",
			journeys: journeys,
			want:     []string{`journey "core" neither drives nor exempts registered agent "codex"`},
		},
		{
			name:       "a blank reason",
			journeys:   journeys,
			exemptions: map[string]map[string]string{"core": {"codex": " "}},
			want:       []string{`journey "core" exempts agent "codex" with no recorded reason`},
		},
		{
			name:       "an exemption for a covered pair",
			journeys:   journeys,
			exemptions: map[string]map[string]string{"core": {"codex": "later", "opencode2": "stale"}},
			want:       []string{`journey "core" drives agent "opencode2" but still exempts it`},
		},
		{
			name:       "an unknown harness",
			journeys:   []Journey{{Name: "core", Harnesses: []string{"opencode2", "nope"}}},
			exemptions: map[string]map[string]string{"core": {"codex": "later"}},
			want:       []string{`journey "core" names unknown agent "nope"`},
		},
		{
			name:     "an exemption for an unknown journey or agent",
			journeys: journeys,
			exemptions: map[string]map[string]string{
				"core":  {"codex": "later", "nope": "x"},
				"other": {"codex": "x"},
			},
			want: []string{
				`exemption for journey "core" names unknown agent "nope"`,
				`exemptions name unknown journey "other"`,
			},
		},
		{
			name:       "a journey with no harness",
			journeys:   []Journey{{Name: "core"}},
			exemptions: map[string]map[string]string{"core": {"codex": "later", "opencode2": "later"}},
			want:       []string{`journey "core" lists no harness`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := journeyCoverageProblems(tc.journeys, tc.exemptions, []string{"codex", "opencode2"})
			if !slices.Equal(got, tc.want) {
				t.Errorf("problems = %q, want %q", got, tc.want)
			}
		})
	}
}

// journeyCoverageProblems returns the completeness problems of journeys against
// exemptions over the registered agent names, sorted.
func journeyCoverageProblems(
	journeys []Journey,
	exemptions map[string]map[string]string,
	registered []string,
) []string {
	var problems []string
	known := make(map[string]bool, len(journeys))
	for _, journey := range journeys {
		known[journey.Name] = true
		problems = append(problems, journeyPairProblems(journey, exemptions[journey.Name], registered)...)
	}
	for journeyName, reasons := range exemptions {
		if !known[journeyName] {
			problems = append(problems, fmt.Sprintf("exemptions name unknown journey %q", journeyName))
			continue
		}
		for name := range reasons {
			if !slices.Contains(registered, name) {
				problems = append(problems,
					fmt.Sprintf("exemption for journey %q names unknown agent %q", journeyName, name))
			}
		}
	}
	slices.Sort(problems)
	return problems
}

// journeyPairProblems returns the problems of one journey's (journey, agent)
// pairs: an unknown or missing harness, an uncovered and unexempted agent, a
// blank reason, and an exemption for a pair the journey drives.
func journeyPairProblems(journey Journey, reasons map[string]string, registered []string) []string {
	var problems []string
	if len(journey.Harnesses) == 0 {
		problems = append(problems, fmt.Sprintf("journey %q lists no harness", journey.Name))
	}
	for _, name := range journey.Harnesses {
		if !slices.Contains(registered, name) {
			problems = append(problems, fmt.Sprintf("journey %q names unknown agent %q", journey.Name, name))
		}
	}
	for _, name := range registered {
		reason, exempted := reasons[name]
		driven := slices.Contains(journey.Harnesses, name)
		switch {
		case driven && exempted:
			problems = append(problems,
				fmt.Sprintf("journey %q drives agent %q but still exempts it", journey.Name, name))
		case driven:
		case !exempted:
			problems = append(problems,
				fmt.Sprintf("journey %q neither drives nor exempts registered agent %q", journey.Name, name))
		case strings.TrimSpace(reason) == "":
			problems = append(problems,
				fmt.Sprintf("journey %q exempts agent %q with no recorded reason", journey.Name, name))
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
			if !slices.Equal(journey.Harnesses, []string{opencode2AgentName}) {
				t.Errorf("attribution journey harnesses = %v, want [%s]", journey.Harnesses, opencode2AgentName)
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
// failure names the step, so every step carries a non-empty Name, a keyword, and
// a Run, and a journey is non-empty.
func TestEveryJourneyStepIsNamedAndRunnable(t *testing.T) {
	for _, journey := range Journeys {
		if strings.TrimSpace(journey.Name) == "" {
			t.Errorf("a journey has no name")
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
			if step.Keyword == "" {
				t.Errorf("journey %q step %q has no Given/When/Then keyword", journey.Name, step.Name)
			}
		}
	}
}

package learn

import (
	"context"
	"sort"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/analyser"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// readBack reads a home volume back. It is a seam so a test drives the after-run
// without a real home volume.
var readBack = readback.Read

// applyFindings applies the derived findings. A seam for the same reason.
var applyFindings = analyser.Apply

// Analysis is everything the after-run needs; the live path and config learn
// both build it.
type Analysis struct {
	Key          state.Key
	Slug         string // repo slug: the per-slug scope and the decline key
	CheckoutSlug string // the artefact store's directory
	Agent        string
	Paths        configpaths.ConfigPaths
	Home         string // home-volume host path
	Baseline     readback.Manifest
	Observation  channel.Observation
	Current      network.Policy // the pre-overlay policy (never-widen diff)
	// ReadBack is the home read-back the findings derive from. The live path and
	// config learn leave it zero; Analyse fills it before deriving the findings.
	ReadBack readback.ReadBack
}

// Findings derives every finding the analysis supports (network + secrets).
func (a Analysis) Findings() []analyser.Finding {
	return analyser.ObservationFindings(analyser.SessionFacts{
		Paths:       a.Paths,
		Slug:        a.Slug,
		ReadBack:    a.ReadBack,
		Observation: a.Observation,
		Current:     a.Current,
	})
}

// SecretValue resolves a proposed static secret's value in memory, matched by
// env-var name; the value is never persisted.
func (a Analysis) SecretValue() func(analyser.SecretWrite) (string, bool) {
	return func(write analyser.SecretWrite) (string, bool) {
		for _, credential := range a.ReadBack.Static {
			if credential.EnvVar == write.EnvVar {
				return credential.Value, credential.Value != ""
			}
		}
		return "", false
	}
}

// Summary is what the exit prompt reports: how many services the session
// reached, how many static keys the proposal saved, the denied destinations,
// and every finding's outcome.
type Summary struct {
	ServicesReached int
	KeysUsed        int
	Denied          []string
	Results         []analyser.ApplyResult
}

// Analyse reads the home back, derives the findings, and applies them
// interactively (the exit prompt decides which). It returns the summary.
func Analyse(
	ctx context.Context,
	a Analysis,
	ui termio.UI,
	mode options.Mode,
	setByFlag func(string) bool,
) (Summary, error) {
	return analyse(a, ui, mode, setByFlag, false)
}

// analyse is the shared body of Analyse and the live after-run's Save all:
// acceptAll applies every finding without asking.
func analyse(
	a Analysis,
	ui termio.UI,
	mode options.Mode,
	setByFlag func(string) bool,
	acceptAll bool,
) (Summary, error) {
	readBackState, err := readBack(a.Home, a.Agent, a.Baseline)
	if err != nil {
		return Summary{}, err
	}
	a.ReadBack = readBackState

	results := applyFindings(a.Findings(), analyser.ApplyOptions{
		Paths:        a.Paths,
		Slug:         a.Slug,
		CheckoutSlug: a.CheckoutSlug,
		UI:           ui,
		Mode:         mode,
		SetByFlag:    setByFlag,
		SecretValue:  a.SecretValue(),
		AcceptAll:    acceptAll,
	})
	return summarize(a, results), nil
}

// summarize counts the services the session reached and the static keys the
// proposal saved, and carries the denied destinations and every outcome.
func summarize(a Analysis, results []analyser.ApplyResult) Summary {
	services := map[string]bool{}
	denied := map[string]bool{}
	for _, destination := range a.Observation.Destinations {
		if destination.Host == "" {
			continue
		}
		switch {
		case destination.Denied:
			denied[destination.Host] = true
		case destination.Success:
			services[destination.Host] = true
		}
	}
	return Summary{
		ServicesReached: len(services),
		KeysUsed:        appliedSecretCount(results),
		Denied:          sortedKeys(denied),
		Results:         results,
	}
}

// appliedSecretCount counts the static secret writes the applier actually made,
// so the summary reports what was saved rather than what was proposed.
func appliedSecretCount(results []analyser.ApplyResult) int {
	count := 0
	for _, result := range results {
		if result.Outcome != analyser.OutcomeApplied {
			continue
		}
		for _, write := range result.Finding.Writes {
			if write.Secret != nil && !write.Secret.Remove {
				count++
			}
		}
	}
	return count
}

// sortedKeys returns a set's keys sorted, or nil for an empty set.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil
	}
	return keys
}

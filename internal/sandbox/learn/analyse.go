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

// Findings derives every finding the analysis supports (network, secrets, and
// the session's managed agent config).
func (a Analysis) Findings(logger termio.Logger) []analyser.Finding {
	findings := analyser.ObservationFindings(analyser.SessionFacts{
		Paths:       a.Paths,
		Slug:        a.Slug,
		ReadBack:    a.ReadBack,
		Observation: a.Observation,
		Current:     a.Current,
	})
	return append(findings, analyser.ManagedConfigFindings(analyser.ManagedConfigFacts{
		Paths:         a.Paths,
		Slug:          a.Slug,
		Home:          a.Home,
		Agent:         a.Agent,
		ReadBack:      a.ReadBack,
		SecretEnvVars: proposedSecretEnvVars(findings),
		Logger:        logger,
	})...)
}

// proposedSecretEnvVars returns the env-var names the observation findings
// propose as static secrets, so the managed-config write-back substitutes a
// credential only when a matching env.secret.yaml entry is proposed.
func proposedSecretEnvVars(findings []analyser.Finding) []string {
	var names []string
	for _, finding := range findings {
		for _, write := range finding.Writes {
			if write.Secret != nil && !write.Secret.Remove {
				names = append(names, write.Secret.EnvVar)
			}
		}
	}
	return names
}

// SecretValue resolves a proposed static secret's value in memory, matched by
// env-var name, from a static credential or a plain-text login key; the value
// is never persisted beyond the secret it is written to. An earlier same-env-var credential
// with an empty value is skipped, so a non-empty source still resolves.
func (a Analysis) SecretValue() func(analyser.SecretWrite) (string, bool) {
	return func(write analyser.SecretWrite) (string, bool) {
		for _, credential := range a.ReadBack.Static {
			if credential.EnvVar == write.EnvVar && credential.Value != "" {
				return credential.Value, true
			}
		}
		// A key a harness's login stored in plain text moves into its secret.
		for _, exposed := range a.ReadBack.Exposed {
			if exposed.EnvVar == write.EnvVar && exposed.Value != "" {
				return exposed.Value, true
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

	results := applyFindings(a.Findings(ui), analyser.ApplyOptions{
		Paths:        a.Paths,
		Slug:         a.Slug,
		CheckoutSlug: a.CheckoutSlug,
		Agent:        a.Agent,
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
		if destination.Host == "" || destination.AddressOnlyTunnel() {
			continue
		}
		switch {
		case destination.Denied:
			denied[destination.Host] = true
		case destination.Success && !destination.HandshakeFailed():
			services[destination.Host] = true
		}
	}
	// A denied host's accepted tunnel is not use, matching the egress lock.
	for host := range denied {
		delete(services, host)
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

// Package analyser turns the facts a finished learn-mode session established —
// the home read-back and the observation channel — into findings: evidence plus
// one or more proposed, scoped writes. It also applies them interactively.
// See specs/onboarding/learn-mode/findings-and-apply.md.
package analyser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Scope is the config layer one write targets.
type Scope string

const (
	// ScopePerSlugUser is the trusted per-repo-slug user layer
	// (~/.config/agents-sandbox/<repo-slug>/). It is the learn-mode proposal's
	// default because it needs no approval gate, and it is shared by every
	// clone of the remote.
	ScopePerSlugUser Scope = "per-slug-user"
	// ScopeProject is the committed project layer. Learn mode never targets it;
	// config harden writes it through the project trust gate.
	ScopeProject Scope = "project"
)

// Kind is whether a finding needs configuration or observation to be emitted.
// config harden runs the config-only subset; the after-run analyser runs both.
type Kind string

const (
	KindConfig      Kind = "config"
	KindObservation Kind = "observation"
)

// Evidence is the observed basis for a finding: destinations, counts, and
// credential-bearing header NAMES — never a value.
type Evidence struct {
	Summary      string
	Destinations []string
	Denied       []string
	Count        int
	Succeeded    bool
	Channel      string   // "readback" | "proxy"
	HeaderFields []string // credential-bearing header names, never values
}

// canonicalEvidence is the ordering-stable rendering Evidence is hashed over.
// Its field order is the hash contract: reorder it and every stored decline
// stops matching.
type canonicalEvidence struct {
	Summary      string   `json:"summary"`
	Destinations []string `json:"destinations"`
	Denied       []string `json:"denied"`
	Count        int      `json:"count"`
	Succeeded    bool     `json:"succeeded"`
	Channel      string   `json:"channel"`
	HeaderFields []string `json:"headerFields"`
}

// hash returns a stable sha256 hex digest of the evidence alone. It is
// insensitive to the order of the list fields, so two renderings of the same
// observation key the same decline.
//
//deadcode:ignore reason="findings engine; pending S7 run --learn caller wires the analyser"
func (e Evidence) hash() string {
	canonical := canonicalEvidence{
		Summary:      e.Summary,
		Destinations: sortedCopy(e.Destinations),
		Denied:       sortedCopy(e.Denied),
		Count:        e.Count,
		Succeeded:    e.Succeeded,
		Channel:      e.Channel,
		HeaderFields: sortedCopy(e.HeaderFields),
	}
	// Evidence holds only strings, numbers, and bools, so marshalling cannot
	// fail; a digest is still the caller's only handle on the evidence, so a
	// panic beats a silently empty hash.
	rendered, err := json.Marshal(canonical)
	if err != nil {
		panic("analyser: marshal evidence: " + err.Error())
	}
	sum := sha256.Sum256(rendered)
	return hex.EncodeToString(sum[:])
}

// sortedCopy returns a sorted copy of values, leaving the caller's slice (and
// its order) untouched.
//
//deadcode:ignore reason="evidence-hash helper; reached only through the marked Evidence.hash until S7 wires run --learn"
func sortedCopy(values []string) []string {
	if values == nil {
		return nil
	}
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}

// Write is one proposed edit, in its own scope. Remove true deletes ConfigKey
// from that scope's launcher config; Secret targets env.secret.yaml instead.
type Write struct {
	Scope       Scope
	ConfigKey   string
	ConfigValue any // ignored when Remove
	Remove      bool
	Secret      *SecretWrite // nil unless a static secret is proposed
	// Text is the user-facing sentence the applier renders in the accept
	// prompt, beside the finding's evidence. It never carries a secret value.
	Text string
}

// SecretWrite carries no Value field — the value is threaded in memory from
// readback.Credential.Value to the writer and never persisted in a finding.
type SecretWrite struct {
	EnvVar       string
	Provider     string
	Hosts        []string
	HeaderFields []string
	Remove       bool
}

// Finding is one proposed change with the evidence that justifies it. Its
// writes are applied together: a move (remove from one scope, add to another)
// is two writes sharing one piece of evidence.
type Finding struct {
	Evidence     Evidence
	Writes       []Write // applied together; a move is two writes (remove, add)
	Kind         Kind
	EvidenceHash string // keys the decline memory; re-armed when the evidence changes
}

// withEvidenceHash returns the finding stamped with its evidence digest, so a
// decline recorded against it can be recognized while the evidence holds and
// re-armed once the evidence changes.
//
//deadcode:ignore reason="findings engine; pending S7 run --learn caller wires the analyser"
func (f Finding) withEvidenceHash() Finding {
	f.EvidenceHash = f.Evidence.hash()
	return f
}

// Outcome is what the applier did with a finding.
type Outcome string

const (
	OutcomeApplied       Outcome = "applied"
	OutcomeDeclined      Outcome = "declined"
	OutcomeSkipped       Outcome = "skipped"
	OutcomeFailed        Outcome = "failed"
	OutcomeNotApplicable Outcome = "not-applicable"
)

// ApplyResult reports one finding's outcome, the config directories it wrote
// to, and any note explaining a skip or the error that failed it.
type ApplyResult struct {
	Finding   Finding
	Outcome   Outcome
	WroteDirs []string
	Note, Err string
}

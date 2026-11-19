// Package learn owns the learn-mode invocation: it starts the observation
// channel before the VM is prepared, captures the baseline manifest, and runs
// the after-run analysis at teardown. See specs/onboarding/learn-mode.md and its
// observation-channel and home-read-back subspecs.
package learn

import (
	"context"
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// homeVolumePath resolves a project's home-volume host path. It is a seam so a
// test drives CaptureBaseline without a live microsandbox daemon.
var homeVolumePath = readback.HomeVolumePath

// takeManifest takes an agent's baseline manifest. A seam for the same reason.
var takeManifest = readback.TakeManifest

// Run owns one session's observation: the channel (nil when passive), the
// pre-overlay base policy, the captured home-volume path, the baseline manifest,
// the recorded session (updated with the artefact path at teardown), and the
// session start.
type Run struct {
	observer  channel.Observer
	base      network.Policy
	home      string
	baseline  readback.Manifest
	record    state.LearnSession
	startedAt time.Time
}

// Begin starts the observation channel over base and returns the Run that owns
// it. On failure it returns a nil Run and the error; the caller warns and uses
// Passive.
func Begin(base network.Policy) (*Run, error) {
	observer, err := channel.StartObservation("", base)
	if err != nil {
		return nil, err
	}
	//exhaustruct:ignore // home is filled by CaptureBaseline, after the VM is prepared
	return &Run{observer: observer, base: base, startedAt: time.Now().UTC()}, nil
}

// Passive returns a Run with no channel: read-back-only analysis.
func Passive(base network.Policy) *Run {
	//exhaustruct:ignore // a passive Run has no observer and no home yet
	return &Run{observer: nil, base: base, startedAt: time.Now().UTC()}
}

// Overlay is the policy the run applies to its options before the VM is
// prepared: the channel overlay when running, else base unchanged.
func (r *Run) Overlay() network.Policy {
	if r.observer == nil {
		return r.base
	}
	return r.observer.Overlay()
}

// Passive reports whether the channel is unavailable.
func (r *Run) Passive() bool { return r.observer == nil }

// Stop tears the channel down and returns its scrubbed artefact; it is
// idempotent. A passive Run returns the zero Observation.
func (r *Run) Stop() channel.Observation {
	if r.observer == nil {
		return channel.Observation{}
	}
	return r.observer.Stop()
}

// CaptureBaseline resolves the home volume's host path, takes the baseline
// manifest, and records the session in state. It returns the host path so the
// after-run reuses it. It is called after the VM is prepared and before attach.
func (r *Run) CaptureBaseline(ctx context.Context, k state.Key, agentName string) (string, error) {
	home, err := homeVolumePath(ctx, k)
	if err != nil {
		return "", err
	}
	homeState, err := state.ReadState(k)
	if err != nil {
		return "", fmt.Errorf("read state for %s/%s: %w", k.Slug, k.Agent, err)
	}
	manifest, err := takeManifest(home, agentName)
	if err != nil {
		return "", err
	}
	r.home = home
	r.baseline = manifest
	//exhaustruct:ignore // ArtefactPath is set at teardown, when the artefact is saved
	r.record = state.LearnSession{
		StartedAt:  r.startedAt,
		HomeVolume: homeState.HomeVolume,
		Baseline:   manifestToState(manifest),
	}
	if err := state.RecordLearnSession(k, r.record); err != nil {
		return "", err
	}
	return home, nil
}

// manifestToState mirrors a read-back manifest into the state package's entry
// type, which exists so a session record carries no readback import.
func manifestToState(manifest readback.Manifest) []state.LearnManifestEntry {
	if len(manifest.Entries) == 0 {
		return nil
	}
	entries := make([]state.LearnManifestEntry, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entries = append(entries, state.LearnManifestEntry{
			Key:     entry.Key,
			Size:    entry.Size,
			ModTime: entry.ModTime,
			Hash:    entry.Hash,
		})
	}
	return entries
}

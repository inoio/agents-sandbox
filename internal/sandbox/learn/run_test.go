package learn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// stubLearnReadback swaps the read-back seams so a test drives CaptureBaseline
// without a live microsandbox daemon or a real home volume.
func stubLearnReadback(t *testing.T, home string, manifest readback.Manifest) {
	t.Helper()
	origHome, origManifest := homeVolumePath, takeManifest
	homeVolumePath = func(context.Context, state.Key) (string, error) { return home, nil }
	takeManifest = func(string, string) (readback.Manifest, error) { return manifest, nil }
	t.Cleanup(func() { homeVolumePath, takeManifest = origHome, origManifest })
}

func seedHomeVolume(t *testing.T, k state.Key, volume string) {
	t.Helper()
	if err := state.WithHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		cur.HomeVolume = volume
		return true, nil
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
}

func TestBeginOverlayChangesThePolicy(t *testing.T) {
	base := network.Policy{Profiles: []network.Profile{network.ProfileNone}}

	run, err := Begin(base)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer run.Stop()

	if run.Passive() {
		t.Error("Begin returned a passive Run, want an active channel")
	}
	overlay := run.Overlay()
	if overlay.Fingerprint() == base.Fingerprint() {
		t.Error("the overlay must change the policy fingerprint, so the VM is recreated")
	}
	if overlay.InterceptTLS != network.InterceptOn {
		t.Errorf("overlay InterceptTLS = %q, want %q", overlay.InterceptTLS, network.InterceptOn)
	}
}

func TestPassiveOverlayIsBaseUnchanged(t *testing.T) {
	base := network.Policy{
		Profiles:    []network.Profile{network.ProfileNone},
		EgressAllow: []string{"existing.example.com"},
	}

	run := Passive(base)
	if !run.Passive() {
		t.Error("Passive Run reports not passive")
	}
	overlay := run.Overlay()
	if overlay.Fingerprint() != base.Fingerprint() {
		t.Error("a passive overlay must be the base policy unchanged")
	}
	if len(overlay.EgressAllow) != 1 || overlay.EgressAllow[0] != "existing.example.com" {
		t.Errorf("passive overlay EgressAllow = %v, want the base's", overlay.EgressAllow)
	}
	if obs := run.Stop(); len(obs.Destinations) != 0 || len(obs.Attribution) != 0 {
		t.Errorf("passive Stop = %+v, want the zero Observation", obs)
	}
}

func TestCaptureBaselineRecordsTheSession(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedHomeVolume(t, k, "vol-1")

	manifest := readback.Manifest{Entries: []readback.ManifestEntry{
		{Key: ".config/opencode/opencode.jsonc", Size: 12, Hash: "h1"},
		{Key: "opencode2:opencode2-sqlite", Hash: "p1"},
	}}
	stubLearnReadback(t, "/home/vol", manifest)

	run := Passive(network.Policy{})
	home, err := run.CaptureBaseline(context.Background(), k, "opencode")
	if err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}
	if home != "/home/vol" {
		t.Errorf("home = %q, want /home/vol", home)
	}

	recorded, ok, err := state.ReadLearnSession(k)
	if err != nil || !ok {
		t.Fatalf("ReadLearnSession: ok=%v err=%v", ok, err)
	}
	if recorded.HomeVolume != "vol-1" {
		t.Errorf("HomeVolume = %q, want vol-1", recorded.HomeVolume)
	}
	if recorded.StartedAt.IsZero() {
		t.Error("StartedAt is zero")
	}
	if len(recorded.Baseline) != len(manifest.Entries) {
		t.Fatalf("Baseline = %+v, want %d entries", recorded.Baseline, len(manifest.Entries))
	}
	for i := range manifest.Entries {
		if recorded.Baseline[i].Key != manifest.Entries[i].Key ||
			recorded.Baseline[i].Size != manifest.Entries[i].Size ||
			recorded.Baseline[i].Hash != manifest.Entries[i].Hash {
			t.Errorf("Baseline[%d] = %+v, want %+v", i, recorded.Baseline[i], manifest.Entries[i])
		}
	}
}

func TestCaptureBaselineNoHomeVolume(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}

	origHome := homeVolumePath
	homeVolumePath = func(context.Context, state.Key) (string, error) {
		return "", fmt.Errorf("no home volume: %w", readback.ErrNoHomeVolume)
	}
	t.Cleanup(func() { homeVolumePath = origHome })

	run := Passive(network.Policy{})
	if _, err := run.CaptureBaseline(context.Background(), k, "opencode"); !errors.Is(err, readback.ErrNoHomeVolume) {
		t.Fatalf("CaptureBaseline error = %v, want readback.ErrNoHomeVolume", err)
	}
	if _, ok, err := state.ReadLearnSession(k); err != nil || ok {
		t.Errorf("ReadLearnSession = ok=%v err=%v, want nothing recorded", ok, err)
	}
}

// TestBeginPropagatesStartObservationFailure pins that Begin returns the nil Run
// and the channel's error, so the caller warns and falls back to Passive rather
// than driving a Run around a channel that never started.
func TestBeginPropagatesStartObservationFailure(t *testing.T) {
	orig := startObservation
	startObservation = func(string, network.Policy) (channel.Observer, error) {
		return nil, errors.New("bind boom")
	}
	t.Cleanup(func() { startObservation = orig })

	run, err := Begin(network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if err == nil || !strings.Contains(err.Error(), "bind boom") {
		t.Fatalf("Begin error = %v, want the channel failure", err)
	}
	if run != nil {
		t.Errorf("Begin returned a Run %+v, want nil on failure", run)
	}
}

// TestCaptureBaselineStateReadFailure pins that a state document that cannot be
// read (not merely missing) is reported, naming the key, rather than being
// treated as "no home volume".
func TestCaptureBaselineStateReadFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedHomeVolume(t, k, "vol-alt")
	stubLearnReadback(t, "/home/alt", readback.Manifest{})
	testutil.WritePath(t, filepath.Join(state.KeyDir(k), "state.yaml"), "{ corrupt: [\n")

	run := Passive(network.Policy{})
	_, err := run.CaptureBaseline(context.Background(), k, "opencode")
	if err == nil || !strings.Contains(err.Error(), "read state for proj/opencode") {
		t.Fatalf("CaptureBaseline error = %v, want the wrapped state read failure", err)
	}
}

// TestCaptureBaselineManifestFailure pins that a manifest failure aborts before
// the session is recorded.
func TestCaptureBaselineManifestFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedHomeVolume(t, k, "vol-1")

	origHome, origManifest := homeVolumePath, takeManifest
	homeVolumePath = func(context.Context, state.Key) (string, error) { return "/home/vol", nil }
	takeManifest = func(string, string) (readback.Manifest, error) {
		return readback.Manifest{}, errors.New("manifest boom")
	}
	t.Cleanup(func() { homeVolumePath, takeManifest = origHome, origManifest })

	run := Passive(network.Policy{})
	if _, err := run.CaptureBaseline(context.Background(), k, "opencode"); err == nil ||
		!strings.Contains(err.Error(), "manifest boom") {
		t.Fatalf("CaptureBaseline error = %v, want the manifest failure", err)
	}
	if _, ok, err := state.ReadLearnSession(k); err != nil || ok {
		t.Errorf("ReadLearnSession = ok=%v err=%v, want nothing recorded", ok, err)
	}
}

// TestCaptureBaselineRecordFailure pins that a failure to record the session is
// returned, so the caller does not proceed to attach believing the after-run
// will find a record.
func TestCaptureBaselineRecordFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedHomeVolume(t, k, "vol-1")
	stubLearnReadback(t, "/home/vol", readback.Manifest{})

	orig := recordLearnSession
	recordLearnSession = func(state.Key, state.LearnSession) error { return errors.New("record boom") }
	t.Cleanup(func() { recordLearnSession = orig })

	run := Passive(network.Policy{})
	if _, err := run.CaptureBaseline(context.Background(), k, "opencode"); err == nil ||
		!strings.Contains(err.Error(), "record boom") {
		t.Fatalf("CaptureBaseline error = %v, want the record failure", err)
	}
}

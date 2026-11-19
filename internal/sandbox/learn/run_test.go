package learn

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
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
	base := network.Policy{Profiles: []network.Profile{network.ProfilePublic}}

	run := Passive(base)
	if !run.Passive() {
		t.Error("Passive Run reports not passive")
	}
	if run.Overlay().Fingerprint() != base.Fingerprint() {
		t.Error("a passive overlay must be the base policy unchanged")
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

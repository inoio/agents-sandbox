package volume

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestEnsureNewHomeSeedsFingerprints pins that a fresh home volume's state
// carries the desired config fingerprints, so it is never fingerprint-less
// beside the VM created from it (issue #183).
func TestEnsureNewHomeSeedsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}
	k := state.Key{Slug: "seedproj", Agent: "opencode"}
	desired := state.Fingerprints{
		Network: state.BuildNetworkState(network.Policy{Profile: network.ProfileNone}),
		Mount:   state.BuildMountState(mounts.Mounts{"/host/data": {Source: "/host/data"}}),
	}

	_, hs, err := NewManager(ui).EnsureNewHome(
		context.Background(),
		client,
		k,
		image.ImageInfo{Tag: "img:tag", Digest: "sha256:digest"},
		true, // dryRunVM
		desired,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureNewHome: %v", err)
	}
	if !reflect.DeepEqual(hs.NetworkState, desired.Network) || !reflect.DeepEqual(hs.MountState, desired.Mount) {
		t.Errorf("returned state not seeded: got %+v, want %+v", hs, desired)
	}

	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !reflect.DeepEqual(onDisk.NetworkState, desired.Network) ||
		!reflect.DeepEqual(onDisk.MountState, desired.Mount) {
		t.Errorf("persisted state not seeded: got %+v, want %+v", onDisk, desired)
	}
}

// TestResolveHomeVolumeRepairsCorruptState pins that a malformed state file is
// replaced by recovery: ResolveHomeVolume routes it to the recovered volume,
// and the rewrite must not fail on the same broken document it is replacing.
func TestResolveHomeVolumeRepairsCorruptState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}
	k := state.Key{Slug: "corruptproj", Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.KeyDir(k), "state.yaml"), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	volName, hs, err := NewManager(ui).ResolveHomeVolume(
		context.Background(),
		client,
		k,
		image.ImageInfo{Tag: "img:tag", Digest: "sha256:digest"},
		true,
		state.Fingerprints{},
		"home-recovered",
		ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if volName != "home-recovered" || hs.HomeVolume != "home-recovered" {
		t.Fatalf("recovered volume = %q / state home = %q, want home-recovered", volName, hs.HomeVolume)
	}

	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after recovery = %v, want the corrupt state repaired", err)
	}
	if onDisk.HomeVolume != "home-recovered" {
		t.Errorf("persisted HomeVolume = %q, want home-recovered", onDisk.HomeVolume)
	}
}

// TestResolveHomeVolumeRepairCorruptSeedsFingerprintsAndClearsUpgradeFields pins
// the lost/corrupt recovery contract: the repaired state is seeded with the
// desired config fingerprints (never fingerprint-less beside a live VM) and its
// upgrade fields are left empty, self-healed on boot by recordImageProvenance;
// offered_versions loss is the accepted cost.
func TestResolveHomeVolumeRepairCorruptSeedsFingerprintsAndClearsUpgradeFields(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}
	k := state.Key{Slug: "corruptseed", Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.KeyDir(k), "state.yaml"), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	desired := state.Fingerprints{
		Network: state.BuildNetworkState(network.Policy{Profile: network.ProfileNone}),
		Mount:   state.BuildMountState(mounts.Mounts{"/host/data": {Source: "/host/data"}}),
	}
	_, hs, err := NewManager(ui).ResolveHomeVolume(
		context.Background(),
		client,
		k,
		image.ImageInfo{Tag: "img:tag", Digest: "sha256:digest"},
		true,
		desired,
		"home-recovered",
		ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if !reflect.DeepEqual(hs.NetworkState, desired.Network) || !reflect.DeepEqual(hs.MountState, desired.Mount) {
		t.Errorf("repaired state not seeded: got %+v, want %+v", hs, desired)
	}
	if hs.AgentVersion != "" || hs.AgentSource != "" || hs.DockerSource != "" || len(hs.OfferedVersions) != 0 {
		t.Errorf("upgrade fields must be left empty on a corrupt-state repair: %+v", hs)
	}

	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after recovery = %v, want the corrupt state repaired", err)
	}
	if !reflect.DeepEqual(onDisk.NetworkState, desired.Network) {
		t.Errorf("persisted state not seeded: got %+v, want %+v", onDisk, desired)
	}
}

// TestResolveHomeVolumeExistingStateKeepsFingerprints pins that the existing
// path is not reseeded: a stored fingerprint (possibly stale) must survive so
// the reconfig decision can detect a real change.
func TestResolveHomeVolumeExistingStateKeepsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}
	k := state.Key{Slug: "existingproj", Agent: "opencode"}
	stored := state.BuildNetworkState(
		network.Policy{Profile: network.ProfilePublic, EgressAllow: []string{"example.com"}},
	)
	if err := state.WriteState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc",
		NetworkState: stored,
	}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	desired := state.Fingerprints{
		Network: state.BuildNetworkState(network.Policy{Profile: network.ProfileNone}),
	}
	_, hs, err := NewManager(ui).ResolveHomeVolume(
		context.Background(),
		client,
		k,
		image.ImageInfo{Tag: "img:tag", Digest: "sha256:abc"},
		true,
		desired,
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if !reflect.DeepEqual(hs.NetworkState, stored) {
		t.Errorf("existing fingerprint was overwritten: got %+v, want %+v", hs.NetworkState, stored)
	}
}

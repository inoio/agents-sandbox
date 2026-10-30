package volume

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// volumeNotFoundError is the SDK's "volume does not exist" error, the only
// lookup failure a caller may treat as true absence. A generic error is a
// transient failure and must fail closed instead.
func volumeNotFoundError(name string) error {
	return &msbSdk.Error{Kind: msbSdk.ErrVolumeNotFound, Message: name}
}

// noCreateVolumeMock returns an msb mock whose GetVolume honours names, and
// whose CreateVolume fails the test: a preview must never create a volume.
func noCreateVolumeMock(t *testing.T, present ...string) *msb.MockMsbClient {
	t.Helper()
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if slices.Contains(present, name) {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		return nil, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must never be called by a preview (got %q)", name)
		panic("unreachable")
	}
	return mock
}

func TestPreviewHomeVolumeReusesRecordedPresentVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatal(err)
	}
	mock := noCreateVolumeMock(t, "vol-1")

	gotName, gotCreate, err := PreviewHomeVolume(context.Background(), mock, k, "")
	if err != nil {
		t.Fatalf("PreviewHomeVolume: %v", err)
	}
	if gotName != "vol-1" || gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (%q, false)", gotName, gotCreate, "vol-1")
	}
}

// TestPreviewHomeVolumeRecordedBeatsRecovered pins the precedence the real
// ResolveHomeVolume uses: a recorded volume that still exists wins over a
// recovered candidate, so a branch swap cannot silently prefer the live mount.
func TestPreviewHomeVolumeRecordedBeatsRecovered(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatal(err)
	}
	mock := noCreateVolumeMock(t, "vol-1", "live-1")

	gotName, gotCreate, err := PreviewHomeVolume(context.Background(), mock, k, "live-1")
	if err != nil {
		t.Fatalf("PreviewHomeVolume: %v", err)
	}
	if gotName != "vol-1" || gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (%q, false)", gotName, gotCreate, "vol-1")
	}
}

func TestPreviewHomeVolumeReusesRecoveredVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	mock := noCreateVolumeMock(t, "live-1")

	gotName, gotCreate, err := PreviewHomeVolume(context.Background(), mock, k, "live-1")
	if err != nil {
		t.Fatalf("PreviewHomeVolume: %v", err)
	}
	if gotName != "live-1" || gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (%q, false)", gotName, gotCreate, "live-1")
	}
}

// TestPreviewHomeVolumeRecordedVolumeGoneFallsBackToRecovered pins the
// precedence ResolveHomeVolume uses: a recorded volume that no longer exists
// does not win over a live recovered one.
func TestPreviewHomeVolumeRecordedVolumeGoneFallsBackToRecovered(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-gone"}); err != nil {
		t.Fatal(err)
	}
	mock := noCreateVolumeMock(t, "live-1")

	gotName, gotCreate, err := PreviewHomeVolume(context.Background(), mock, k, "live-1")
	if err != nil {
		t.Fatalf("PreviewHomeVolume: %v", err)
	}
	if gotName != "live-1" || gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (%q, false)", gotName, gotCreate, "live-1")
	}
}

// TestPreviewHomeVolumeReportsCreateWhenNonePresent pins that the create branch
// names no volume: a fresh volume's name embeds a timestamp, so there is no
// single name the preview could report.
func TestPreviewHomeVolumeReportsCreateWhenNonePresent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	mock := noCreateVolumeMock(t)

	gotName, gotCreate, err := PreviewHomeVolume(context.Background(), mock, k, "")
	if err != nil {
		t.Fatalf("PreviewHomeVolume: %v", err)
	}
	if gotName != "" || !gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (\"\", true)", gotName, gotCreate)
	}
}

// TestPreviewHomeVolumeReportsCreateWhenRecoveredVolumeMissing pins that an
// unusable recovered candidate is not adopted.
func TestPreviewHomeVolumeReportsCreateWhenRecoveredVolumeMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	mock := noCreateVolumeMock(t)

	gotName, gotCreate, err := PreviewHomeVolume(context.Background(), mock, k, "live-gone")
	if err != nil {
		t.Fatalf("PreviewHomeVolume: %v", err)
	}
	if gotName != "" || !gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (\"\", true)", gotName, gotCreate)
	}
}

// TestPreviewHomeVolumeTransientErrorFailsClosed pins that a lookup error that
// is not true absence is surfaced rather than reported as "would create",
// matching the real run's fail-closed behavior. Both the recorded-volume and
// the recovered-candidate lookups must fail closed.
func TestPreviewHomeVolumeTransientErrorFailsClosed(t *testing.T) {
	transient := func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("daemon unavailable looking up %s", name)
	}

	t.Run("recorded volume", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		k := state.Key{Slug: "previewtransient", Agent: "opencode"}
		if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-transient"}); err != nil {
			t.Fatal(err)
		}
		mock := &msb.MockMsbClient{GetVolumeFn: transient}

		if _, _, err := PreviewHomeVolume(context.Background(), mock, k, "live-1"); err == nil {
			t.Fatal("PreviewHomeVolume returned no error for a transient recorded-volume lookup, want fail-closed")
		}
	})

	t.Run("recovered candidate", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		k := state.Key{Slug: "previewtransient", Agent: "opencode"}
		mock := &msb.MockMsbClient{GetVolumeFn: transient}

		if _, _, err := PreviewHomeVolume(context.Background(), mock, k, "live-1"); err == nil {
			t.Fatal("PreviewHomeVolume returned no error for a transient recovery lookup, want fail-closed")
		}
	})
}

// TestPreviewHomeVolumeCorruptStateReportsCreate covers a state file that
// cannot be read: the preview must report a create rather than fail, and leave
// the corrupt document untouched for the real run to repair.
func TestPreviewHomeVolumeCorruptStateReportsCreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "corruptpreview", Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(state.KeyDir(k), "state.yaml")
	if err := os.WriteFile(statePath, []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := noCreateVolumeMock(t)

	gotName, gotCreate, err := PreviewHomeVolume(context.Background(), mock, k, "")
	if err != nil {
		t.Fatalf("PreviewHomeVolume: %v", err)
	}
	if gotName != "" || !gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (\"\", true)", gotName, gotCreate)
	}

	onDisk, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state file after preview: %v", err)
	}
	if string(onDisk) != "{ corrupt: [\n" {
		t.Errorf("preview rewrote the state file: %q", onDisk)
	}
}

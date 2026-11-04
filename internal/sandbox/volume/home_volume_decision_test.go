package volume

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestHomeVolumeResolution_PreviewMatchesRealDecision drives PreviewHomeVolume
// and (*Manager).ResolveHomeVolume over the same inputs and pins that both take
// the same reuse → recover → create decision. It guards against the two
// implementations drifting apart (#527): a change to the resolution order that
// lands in only one entry point fails here.
func TestHomeVolumeResolution_PreviewMatchesRealDecision(t *testing.T) {
	const (
		wantReuse   = "reuse"
		wantRecover = "recover"
		wantCreate  = "create"
		wantError   = "error"
	)

	tests := []struct {
		name          string
		seed          bool
		recorded      string
		adoption      string
		present       []string
		liveCandidate string
		transient     bool
		want          string
		wantVolume    string
	}{
		{
			name: "recorded volume present",
			seed: true, recorded: "vol-1",
			present: []string{"vol-1"},
			want:    wantReuse, wantVolume: "vol-1",
		},
		{
			name:          "live volume recovered when state missing",
			liveCandidate: "live-1",
			present:       []string{"live-1"},
			want:          wantRecover, wantVolume: "live-1",
		},
		{
			name: "live volume recovered when recorded volume gone",
			seed: true, recorded: "vol-gone",
			liveCandidate: "live-1",
			present:       []string{"live-1"},
			want:          wantRecover, wantVolume: "live-1",
		},
		{
			name: "adoption marker recovered",
			seed: true, adoption: "mark-1",
			present: []string{"mark-1"},
			want:    wantRecover, wantVolume: "mark-1",
		},
		{
			name: "fresh volume when nothing survives",
			want: wantCreate,
		},
		{
			name: "fresh volume when recorded and live are gone",
			seed: true, recorded: "vol-gone",
			liveCandidate: "live-gone",
			want:          wantCreate,
		},
		{
			name: "fresh volume when adoption marker is gone",
			seed: true, adoption: "mark-gone",
			want: wantCreate,
		},
		{
			name: "recorded volume transient error",
			seed: true, recorded: "vol-x",
			transient: true,
			want:      wantError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			k := state.Key{Slug: "decideproj", Agent: "opencode"}
			if tt.seed {
				if err := seedHomeState(k, state.HomeState{
					HomeVolume:          tt.recorded,
					RecoveredHomeVolume: tt.adoption,
				}); err != nil {
					t.Fatal(err)
				}
			}

			previewName, previewCreate, previewErr := PreviewHomeVolume(
				context.Background(), lookupMock(tt.present, tt.transient), k, tt.liveCandidate,
			)
			if (previewErr != nil) != (tt.want == wantError) {
				t.Fatalf("PreviewHomeVolume err = %v, want error=%v", previewErr, tt.want == wantError)
			}

			created := false
			realMock := lookupMock(tt.present, tt.transient)
			realMock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
				created = true
				return msb.MockVolumeHandle{Name_: name}, nil
			}
			docker.WithNoopDockerMock(t)
			realName, _, realErr := NewManager(docker.Get()).ResolveHomeVolume(
				context.Background(), realMock, k,
				image.ImageInfo{Digest: "sha256:def"},
				false, state.Fingerprints{}, tt.liveCandidate, &termio.Mock{},
			)
			if (realErr != nil) != (tt.want == wantError) {
				t.Fatalf("ResolveHomeVolume err = %v, want error=%v", realErr, tt.want == wantError)
			}
			if tt.want == wantError {
				return
			}

			if previewCreate != created {
				t.Fatalf("preview create = %v, real created = %v: decisions differ", previewCreate, created)
			}
			if created {
				if previewName != "" {
					t.Errorf("preview named %q for a volume it would create; want no name", previewName)
				}
			} else if previewName != realName {
				t.Errorf("preview volume = %q, real volume = %q: decisions differ", previewName, realName)
			}

			switch tt.want {
			case wantReuse, wantRecover:
				if created {
					t.Fatalf("real run created a volume, want %s %q", tt.want, tt.wantVolume)
				}
				if previewName != tt.wantVolume || realName != tt.wantVolume {
					t.Errorf(
						"decision = preview %q / real %q, want %s %q",
						previewName,
						realName,
						tt.want,
						tt.wantVolume,
					)
				}
			case wantCreate:
				if !created {
					t.Fatal("real run did not create a volume, want create")
				}
				if !previewCreate {
					t.Error("preview reported reuse/recover, want create")
				}
			}
		})
	}
}

// lookupMock returns an msb mock whose GetVolume honours present (everything
// else is the SDK's volume-not-found), or a transient error for every name when
// transient is set.
func lookupMock(present []string, transient bool) *msb.MockMsbClient {
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if transient {
			var vh msb.VolumeHandle
			return vh, fmt.Errorf("daemon unavailable looking up %s", name)
		}
		if slices.Contains(present, name) {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		return nil, volumeNotFoundError(name)
	}
	return mock
}

// TestResolveHomeVolume_ReuseWarnsNothing pins that a recorded volume that still
// resolves is reused silently: the real path neither reports a state-read
// failure nor falls through to the recovery warning.
func TestResolveHomeVolume_ReuseWarnsNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "quietreuse", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{HomeVolume: "vol-keep"}); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	_, hs, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), lookupMock([]string{"vol-keep"}, false), key,
		image.ImageInfo{Digest: "sha256:def"}, false, state.Fingerprints{}, "", ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if hs.HomeVolume != "vol-keep" {
		t.Errorf("state volume = %q, want vol-keep", hs.HomeVolume)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("reuse emitted warnings: %v", ui.WarnCalls)
	}
}

// TestResolveHomeVolume_AbsentStateWarnsNothing pins that a missing state file
// (ErrStateNotFound) is not reported as a read failure; only a genuinely
// unreadable document is.
func TestResolveHomeVolume_AbsentStateWarnsNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "absentquiet", Agent: "opencode"}

	ui := &termio.Mock{}
	_, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), lookupMock([]string{"live-quiet"}, false), key,
		image.ImageInfo{Digest: "sha256:def"}, false, state.Fingerprints{}, "live-quiet", ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if strings.Contains(strings.Join(ui.WarnCalls, "\n"), "could not read state file") {
		t.Errorf("absent state reported as a read failure: %v", ui.WarnCalls)
	}
}

// TestResolveHomeVolume_RecordedLookupErrorWrapsCause pins that a transient
// error probing the recorded volume fails closed with the cause still reachable
// and the recorded-volume context on the error.
func TestResolveHomeVolume_RecordedLookupErrorWrapsCause(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cause := errors.New("daemon unavailable")
	key := state.Key{Slug: "wrapcause", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{HomeVolume: "vol-wrap"}); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, _ string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("lookup: %w", cause)
	}

	_, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), mock, key,
		image.ImageInfo{Digest: "sha256:def"}, false, state.Fingerprints{}, "", &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected a fail-closed error for the transient recorded-volume lookup")
	}
	if !errors.Is(err, cause) {
		t.Errorf("error %v does not wrap the lookup cause", err)
	}
	if !strings.Contains(err.Error(), "checking home volume") {
		t.Errorf("error %q lacks the recorded-volume context", err)
	}
}

// TestResolveHomeVolume_RecoveryCandidateErrorSurfaces pins that a transient
// error confirming the recovery candidate — after the recorded volume is
// already known gone — is surfaced, not swallowed.
func TestResolveHomeVolume_RecoveryCandidateErrorSurfaces(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cause := errors.New("daemon unavailable")
	key := state.Key{Slug: "candidateerr", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{HomeVolume: "vol-gone"}); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == "vol-gone" {
			return nil, volumeNotFoundError(name)
		}
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("lookup: %w", cause)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called for a transient candidate lookup (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	_, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), mock, key,
		image.ImageInfo{Digest: "sha256:def"}, false, state.Fingerprints{}, "live-x", &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected a fail-closed error for the transient candidate lookup")
	}
	if !errors.Is(err, cause) {
		t.Errorf("error %v does not wrap the candidate lookup cause", err)
	}
}

// TestResolveHomeVolume_ClearsAdoptionClaimWhenMarkerVolumeGone pins #144's
// claim retirement on the create path: when an adoption marker's volume is gone,
// a fresh volume is created and the spent claim is still cleared.
func TestResolveHomeVolume_ClearsAdoptionClaimWhenMarkerVolumeGone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	const marker = "agents-sandbox-home-widget-bbb-opencode-9"
	key := state.Key{Slug: "adoptedwidget2", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{RecoveredHomeVolume: marker}); err != nil {
		t.Fatal(err)
	}
	seedAdoptionClaim(t, "widget-bbb", key.Slug, marker)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}

	if _, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), mock, key,
		image.ImageInfo{Digest: "sha256:new"}, false, state.Fingerprints{}, "", &termio.Mock{},
	); err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}

	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume("widget-bbb", "opencode"); ok {
		t.Errorf("adoption claim survived the create fallback: %+v", root.AdoptedHomeVolumes)
	}
}

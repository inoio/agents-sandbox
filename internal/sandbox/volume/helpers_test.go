package volume

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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

// volumeNotFoundError is the SDK's "volume does not exist" error, the only
// lookup failure a caller may treat as true absence. A generic error is a
// transient failure and must fail closed instead.
func volumeNotFoundError(name string) error {
	return &msbSdk.Error{Kind: msbSdk.ErrVolumeNotFound, Message: name}
}

// seedHomeState writes hs as the key's HomeState through the recovery
// transaction. Tests only.
func seedHomeState(k state.Key, hs state.HomeState) error {
	return state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = hs
		return true, nil
	})
}

// eventLog records the order of lifecycle events across the UI, client and
// sandbox fakes.
type eventLog struct{ events []string }

func (l *eventLog) add(event string) { l.events = append(l.events, event) }

// imageLoadSpinnerLabel is the spinner image.EnsureLoaded shows while loading
// the runner image; it is not part of the throwaway lifecycle under test.
const imageLoadSpinnerLabel = "Loading image into microsandbox"

// recordingUI is a termio.Mock whose spinners log their lifecycle.
type recordingUI struct {
	termio.Mock

	log *eventLog
}

func (u *recordingUI) Spinner(msg string) termio.Spinner {
	if msg == imageLoadSpinnerLabel {
		return u.Mock.Spinner(msg)
	}
	u.log.add("spinner start: " + msg)
	return &recordingSpinner{log: u.log}
}

type recordingSpinner struct{ log *eventLog }

func (s *recordingSpinner) Stop() { s.log.add("spinner stop") }

func (s *recordingSpinner) StopError(err error) { s.log.add("spinner error: " + err.Error()) }

// trackingSandbox is a MockSandbox that logs Destroy and Close attempts,
// including failed ones.
type trackingSandbox struct {
	*msb.MockSandbox

	log *eventLog
}

func (s *trackingSandbox) Destroy(ctx context.Context, opts ...msbSdk.DestroyOption) error {
	s.log.add("destroy")
	return s.MockSandbox.Destroy(ctx, opts...)
}

func (s *trackingSandbox) Close() error {
	s.log.add("close")
	return s.MockSandbox.Close()
}

type throwawayFixture struct {
	log     *eventLog
	ui      *recordingUI
	sandbox *trackingSandbox
	client  *msb.MockMsbClient
}

var throwawayKey = state.Key{Slug: "myproject", Agent: "opencode"}

var throwawayImage = image.ImageInfo{
	DockerTag: "agents-sandbox/runner-test:latest",
	Ref:       "agents-sandbox/runner-test:latest",
}

func (f *throwawayFixture) run(spec throwawaySandbox, payload func(msb.Sandbox, termio.Spinner) error) error {
	return withThrowawaySandbox(
		context.Background(),
		docker.Get(),
		f.client,
		throwawayKey,
		throwawayImage,
		spec,
		f.ui,
		payload,
	)
}

func (f *throwawayFixture) createdConfig(t *testing.T) msbSdk.SandboxConfig {
	t.Helper()
	require.Len(t, f.client.CreatedSandboxCalls, 1)
	var cfg msbSdk.SandboxConfig
	for _, opt := range f.client.CreatedSandboxCalls[0].Opts {
		opt(&cfg)
	}
	return cfg
}

// seedAdoptionClaim records a legacy identity's adoption claim (#144) through the
// locked root-state transaction, the way an adopting checkout does.
func seedAdoptionClaim(t *testing.T, legacySlug, checkoutSlug, volume string) {
	t.Helper()
	if _, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		txn.State().ClaimAdoptedHomeVolume(legacySlug, "opencode", checkoutSlug, volume)
		return txn.Commit()
	}); err != nil {
		t.Fatalf("seed adoption claim: %v", err)
	}
}

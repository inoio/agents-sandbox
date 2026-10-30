package volume

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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

func (s *recordingSpinner) Stop()               { s.log.add("spinner stop") }
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

func newThrowawayFixture(t *testing.T) *throwawayFixture {
	t.Helper()
	docker.WithNoopDockerMock(t)
	log := &eventLog{}
	sandbox := &trackingSandbox{MockSandbox: &msb.MockSandbox{Name_: "throwaway"}, log: log}
	fixture := &throwawayFixture{
		log:     log,
		ui:      &recordingUI{log: log},
		sandbox: sandbox,
	}
	fixture.client = &msb.MockMsbClient{
		CreateSandboxFn: func(context.Context, string, ...msbSdk.SandboxOption) (msb.Sandbox, error) {
			log.add("create")
			return sandbox, nil
		},
	}
	return fixture
}

var throwawayKey = state.Key{Slug: "myproject", Agent: "opencode"}

var throwawayImage = image.ImageInfo{
	DockerTag: "agents-sandbox/runner-test:latest",
	Ref:       "agents-sandbox/runner-test:latest",
}

func (f *throwawayFixture) run(spec throwawaySandbox, payload func(msb.Sandbox, termio.Spinner) error) error {
	return withThrowawaySandbox(context.Background(), f.client, throwawayKey, throwawayImage, spec, f.ui, payload)
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

func TestWithThrowawaySandbox_CreatesLabelledReplaceableTaskSandbox(t *testing.T) {
	fixture := newThrowawayFixture(t)
	mounts := map[string]msbSdk.MountConfig{
		srcMount: msbSdk.Mount.Named("old-vol", msbSdk.MountOptions{}),
		dstMount: msbSdk.Mount.Named("new-vol", msbSdk.MountOptions{}),
	}

	var payloadSandbox msb.Sandbox
	err := fixture.run(throwawaySandbox{purpose: "copy", mounts: mounts}, func(sb msb.Sandbox, _ termio.Spinner) error {
		payloadSandbox = sb
		return nil
	})

	require.NoError(t, err)
	require.Same(t, fixture.sandbox, payloadSandbox)
	name := fixture.client.CreatedSandboxCalls[0].Name
	require.True(t, strings.HasPrefix(name, "myproject-opencode-"), "name = %q", name)
	cfg := fixture.createdConfig(t)
	require.Equal(t, throwawayImage.Ref, cfg.Image)
	require.True(t, cfg.Replace, "throwaway sandbox must be created WithReplace")
	require.Equal(t, naming.SandboxTypeTask, cfg.Labels[naming.LabelType])
	require.Equal(t, "myproject", cfg.Labels[naming.LabelSlug])
	require.Equal(t, "opencode", cfg.Labels[naming.LabelAgent])
	require.Equal(t, mounts, cfg.Volumes)
}

func TestWithThrowawaySandbox_DestroysAndClosesAfterPayload(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		payloadErr error
	}{
		{name: "payload succeeds"},
		{name: "payload fails", payloadErr: errors.New("payload failed")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newThrowawayFixture(t)

			err := fixture.run(throwawaySandbox{purpose: "copy"}, func(msb.Sandbox, termio.Spinner) error {
				fixture.log.add("payload")
				return testCase.payloadErr
			})

			require.ErrorIs(t, err, testCase.payloadErr)
			require.Equal(t, []string{"create", "payload", "destroy", "close"}, fixture.log.events)
			require.Empty(t, fixture.ui.WarnCalls)
		})
	}
}

func TestWithThrowawaySandbox_DestroyErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		destroyErr error
		wantWarn   bool
	}{
		{name: "replaced is silent", destroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"}},
		{name: "not found is silent", destroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}},
		{name: "other error warns", destroyErr: errors.New("destroy failed"), wantWarn: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newThrowawayFixture(t)
			fixture.sandbox.DestroyErr = testCase.destroyErr

			err := fixture.run(
				throwawaySandbox{purpose: "edit"},
				func(msb.Sandbox, termio.Spinner) error { return nil },
			)

			require.NoError(t, err)
			require.Contains(t, fixture.log.events, "close")
			if !testCase.wantWarn {
				require.Empty(t, fixture.ui.WarnCalls)
				return
			}
			require.Len(t, fixture.ui.WarnCalls, 1)
			require.Contains(t, fixture.ui.WarnCalls[0], "failed to destroy edit sandbox")
			require.Contains(t, fixture.ui.WarnCalls[0], "destroy failed")
		})
	}
}

func TestWithThrowawaySandbox_LoadImageFailureSkipsEverything(t *testing.T) {
	fixture := newThrowawayFixture(t)
	docker.WithDefaultErrorDockerMock(t)

	payloadRan := false
	err := fixture.run(
		throwawaySandbox{purpose: "prefill", spinnerLabel: "Preparing"},
		func(msb.Sandbox, termio.Spinner) error {
			payloadRan = true
			return nil
		},
	)

	require.ErrorContains(t, err, "load runner image")
	require.False(t, payloadRan)
	require.NotContains(t, fixture.log.events, "spinner start: Preparing")
	require.NotContains(t, fixture.log.events, "create")
	require.NotContains(t, fixture.log.events, "destroy")
}

func TestWithThrowawaySandbox_CreateFailureStopsSpinnerAndSkipsPayload(t *testing.T) {
	fixture := newThrowawayFixture(t)
	fixture.client.CreateSandboxFn = func(context.Context, string, ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		fixture.log.add("create")
		return nil, errors.New("boom")
	}

	payloadRan := false
	err := fixture.run(
		throwawaySandbox{purpose: "edit", spinnerLabel: "Starting"},
		func(msb.Sandbox, termio.Spinner) error {
			payloadRan = true
			return nil
		},
	)

	require.ErrorContains(t, err, "create edit sandbox: boom")
	require.False(t, payloadRan)
	require.Equal(t, []string{"spinner start: Starting", "create", "spinner error: boom"}, fixture.log.events)
}

func TestWithThrowawaySandbox_SpinnerStartsBeforeCreateAndIsHandedToPayload(t *testing.T) {
	fixture := newThrowawayFixture(t)

	err := fixture.run(
		throwawaySandbox{purpose: "prefill", spinnerLabel: "Preparing"},
		func(_ msb.Sandbox, spin termio.Spinner) error {
			require.NotNil(t, spin)
			spin.Stop()
			return nil
		},
	)

	require.NoError(t, err)
	require.Equal(t,
		[]string{"spinner start: Preparing", "create", "spinner stop", "destroy", "close"},
		fixture.log.events,
	)
}

func TestWithThrowawaySandbox_NoSpinnerLabelStartsNoSpinner(t *testing.T) {
	fixture := newThrowawayFixture(t)

	err := fixture.run(throwawaySandbox{purpose: "copy"}, func(_ msb.Sandbox, spin termio.Spinner) error {
		require.Nil(t, spin)
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"create", "destroy", "close"}, fixture.log.events)
}

func TestRunShell(t *testing.T) {
	const script = "cp -a /src/. /dst/"
	for _, testCase := range []struct {
		name        string
		execOut     msb.ShellResult
		execErr     error
		wantErr     string
		wantSpinner string
	}{
		{name: "success", wantSpinner: "spinner stop"},
		{
			name:        "exec error",
			execErr:     errors.New("no exec"),
			wantErr:     "copy files: no exec",
			wantSpinner: "spinner error: no exec",
		},
		{
			name:        "non-zero exit",
			execOut:     msb.NewTestResult(false, 3, "", "denied", nil),
			wantErr:     "copy failed (exit 3): denied",
			wantSpinner: "spinner error: copy failed (exit 3): denied",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			log := &eventLog{}
			sandbox := &msb.MockSandbox{ExecErr: testCase.execErr}
			if testCase.execOut != nil {
				sandbox.ExecOut = map[string]msb.ShellResult{"sh -c " + script: testCase.execOut}
			}

			err := runShell(context.Background(), sandbox, &recordingSpinner{log: log}, shellStep{
				execErrPrefix: "copy files",
				failurePrefix: "copy failed",
				script:        script,
			})

			if testCase.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, testCase.wantErr)
			}
			require.Equal(t, []string{testCase.wantSpinner}, log.events)
		})
	}
}

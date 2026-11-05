package volume

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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

// TestTaskSandboxNameStaysWithinNameLimit pins the longest realistic task
// sandbox name (a long folder, the hyphenated `claude-code` agent, and a
// 19-digit nanos): it must fit the sandbox-name limit and round-trip to the same
// slug, agent and task flag, so it cannot be truncated into another artifact's
// namespace.
func TestTaskSandboxNameStaysWithinNameLimit(t *testing.T) {
	slug := strings.Repeat("a", 30) + "-1mjusbm3wikhb0"
	name := naming.TaskSandboxName(slug, "claude-code", 1729000000000000000)
	require.LessOrEqual(t, len(name), options.MaxSandboxNameLen, "name %q is too long", name)
	require.Equal(t, naming.ArtifactInfo{Slug: slug, Agent: "claude-code", Task: true}, naming.ParseVMName(name))
}

func TestWithThrowawaySandbox_CreatesLabelledTaskSandbox(t *testing.T) {
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
	require.False(t, cfg.Replace, "a throwaway sandbox never replaces a same-name sandbox")
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
	require.Error(t, errors.Unwrap(err), "the cause must stay wrapped")
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
	require.Error(t, errors.Unwrap(err), "the cause must stay wrapped")
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

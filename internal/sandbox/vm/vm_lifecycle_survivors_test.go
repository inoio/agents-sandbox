package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestLifecycleSpinnerIsFinishedOnPreLockResolution pins that every pre-lock
// transition branch finishes its "Checking project VM" spinner — a successful
// path stops it, a failing one stops it with the error — so a resolution never
// leaves the spinner running behind whatever is printed next.
//
// The post-lock re-check passes a nil spinner, so this covers the half of the
// shared transition whose spinner is non-nil.
func TestLifecycleSpinnerIsFinishedOnPreLockResolution(t *testing.T) {
	cases := []struct {
		name           string
		handle         func() msb.SandboxHandle
		wantErr        bool
		wantStops      int
		wantStopErrors int
	}{
		{
			name: "decide error",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatus("unknown-status")}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "wait error",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   "vm",
					Status_: msbSdk.SandboxStatusStarting,
					RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
						return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusPaused}, nil
					},
				}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "wait success",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   "vm",
					Status_: msbSdk.SandboxStatusStarting,
					RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
						return &msb.MockSandboxHandle{
							Status_:   msbSdk.SandboxStatusRunning,
							ConnectSb: &msb.MockSandbox{Name_: "vm"},
						}, nil
					},
				}
			},
			wantStops: 1,
		},
		{
			name: "start success",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   "vm",
					Status_: msbSdk.SandboxStatusStopped,
					StartSb: &msb.MockSandbox{Name_: "vm"},
				}
			},
			wantStops: 1,
		},
		{
			name: "start error",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:    "vm",
					Status_:  msbSdk.SandboxStatusStopped,
					StartErr: errors.New("start failed"),
				}
			},
			wantErr: true,
			// The spinner is stopped before the start is attempted (it would
			// otherwise animate through it) and then stopped again with the
			// error; the real spinner's second finish is a no-op, the mock counts
			// both.
			wantStops:      1,
			wantStopErrors: 1,
		},
		{
			name: "connect success",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:     "vm",
					Status_:   msbSdk.SandboxStatusRunning,
					ConnectSb: &msb.MockSandbox{Name_: "vm"},
				}
			},
			wantStops: 1,
		},
		{
			name: "connect retry refresh error",
			handle: func() msb.SandboxHandle {
				return &retryConnectHandle{
					SandboxHandle: &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
					refreshErr:    errors.New("refresh failed"),
				}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "connect retry unavailable status",
			handle: func() msb.SandboxHandle {
				return &retryConnectHandle{
					SandboxHandle: &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
					refresh:       &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusPaused},
				}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "connect retry resume error",
			handle: func() msb.SandboxHandle {
				// No RefreshFn, so the retry re-reads the same running status and
				// the second connect fails the same way.
				return &msb.MockSandboxHandle{
					Name_:      "vm",
					Status_:    msbSdk.SandboxStatusRunning,
					ConnectErr: errors.New("connect failed"),
				}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "connect retry reconnect",
			handle: func() msb.SandboxHandle {
				return &retryConnectHandle{
					SandboxHandle: &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
					refresh: &msb.MockSandboxHandle{
						Status_:   msbSdk.SandboxStatusRunning,
						ConnectSb: &msb.MockSandbox{Name_: "vm"},
					},
				}
			},
			wantStops: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			ui := termio.NewTestMock(t)
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(tc.handle())
			msb.WithMsbMock(t, client)

			_, _, err := ensureProjectVM(
				context.Background(),
				options.RunOptions{Agent: "opencode"},
				image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
				"vol",
				"/workspace",
				testVMKey(),
				testVMKey().Slug,
				false,
				envsecret.EnvSecrets{},
				&ui,
			)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ensureProjectVM error = %v, wantErr %v", err, tc.wantErr)
			}
			if ui.SpinnerStops != tc.wantStops {
				t.Errorf("spinner stops = %d, want %d", ui.SpinnerStops, tc.wantStops)
			}
			if len(ui.SpinnerStopErrors) != tc.wantStopErrors {
				t.Errorf("spinner stop errors = %v, want %d", ui.SpinnerStopErrors, tc.wantStopErrors)
			}
		})
	}
}

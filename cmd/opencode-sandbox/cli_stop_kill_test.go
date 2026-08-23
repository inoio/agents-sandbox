package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	msb "github.com/superradcompany/microsandbox/sdk/go"

	sandboxmsb "github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func initTestRepo(t *testing.T) {
	t.Helper()
	dir := testutil.InitRepo(t)
	t.Chdir(dir)
}
func notFoundErr() error {
	return &msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"}
}

// setupStopKillConfig builds a stop/kill command fixture installed with the
// given mock configuration, so callers retain control over Execute's error.
func setupStopKillConfig(
	t *testing.T,
	args []string,
	mockSetup func(*sandboxmsb.MockMsbClient),
) (*cobra.Command, *termio.Mock) {
	t.Helper()
	cmd, ui := setupCommandFixtures(t, args...)
	mock := &sandboxmsb.MockMsbClient{}
	if mockSetup != nil {
		mockSetup(mock)
	}
	sandboxmsb.WithMsbMock(t, mock)
	return cmd, ui
}

// runStopKill executes a stop/kill command and fails the test on any
// unexpected error, returning the UI for assertions.
func runStopKill(t *testing.T, args []string, mockSetup func(*sandboxmsb.MockMsbClient)) *termio.Mock {
	t.Helper()
	cmd, ui := setupStopKillConfig(t, args, mockSetup)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return ui
}

// assertInfoHasPrefix checks that ui.InfoCalls contains at least one entry
// containing prefix. Calls t.Errorf and returns false on failure.
func assertInfoHasPrefix(t *testing.T, ui *termio.Mock, prefix string) {
	t.Helper()
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, prefix) {
			return
		}
	}
	t.Errorf("expected InfoCall containing %q; got: %v", prefix, ui.InfoCalls)
}

// assertVerboseHasPrefix checks that ui.VerboseCalls contains at least
// one entry starting with prefix.
func assertVerboseHasPrefix(t *testing.T, ui *termio.Mock, prefix string) {
	t.Helper()
	for _, call := range ui.VerboseCalls {
		if strings.HasPrefix(call, prefix) {
			return
		}
	}
	t.Errorf("expected VerboseCall starting with %q; got: %v", prefix, ui.VerboseCalls)
}

// assertWarnContains checks that ui.WarnCalls contains at least one entry
// containing needle.
func assertWarnContains(t *testing.T, ui *termio.Mock, needle string) {
	t.Helper()
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, needle) {
			return
		}
	}
	t.Errorf("expected WarnCall containing %q; got: %v", needle, ui.WarnCalls)
}

// assertNoWarn calls t.Errorf if ui.WarnCalls is non-empty.
func assertNoWarn(t *testing.T, ui *termio.Mock) {
	t.Helper()
	if len(ui.WarnCalls) > 0 {
		t.Errorf("expected no WarnCalls; got: %v", ui.WarnCalls)
	}
}

// assertSpinnerHas calls t.Errorf if ui.SpinnerCalls does not contain msg.
func assertSpinnerHas(t *testing.T, ui *termio.Mock, msg string) {
	t.Helper()
	if slices.Contains(ui.SpinnerCalls, msg) {
		return
	}
	t.Errorf("expected SpinnerCall %q; got: %v", msg, ui.SpinnerCalls)
}

func TestStopKillLifecycle(t *testing.T) {
	t.Run("no project VM found", func(t *testing.T) {
		for _, tc := range []struct {
			cmd        string
			wantPrefix string
		}{
			{cmdStop, "no project VM found: "},
			{cmdKill, "no project VM found: "},
		} {
			for _, flags := range stopKillFlags {
				t.Run(strings.Join(append([]string{tc.cmd}, flags...), " "), func(t *testing.T) {
					ui := runStopKill(t, append([]string{tc.cmd}, flags...), func(m *sandboxmsb.MockMsbClient) {
						m.SetGetSandboxErr(notFoundErr())
					})

					assertInfoHasPrefix(t, ui, tc.wantPrefix)
				})
			}
		}
	})

	// dry-run stop/kill, both with and without persisted state removal
	for _, tc := range []struct {
		name       string
		cmd        string
		infoPrefix string
	}{
		{"dry-run stop", cmdStop, "dry-run: Would stop"},
		{"dry-run kill", cmdKill, "dry-run: Would kill "},
		{"dry-run stop removes persisted state", cmdStop, "(also would remove persisted state)"},
		{"dry-run kill removes persisted state", cmdKill, "(also would remove persisted state)"},
	} {
		for _, flags := range stopKillFlags {
			t.Run(tc.name+" "+strings.Join(flags, " "), func(t *testing.T) {
				initTestRepo(t)
				ui := runStopKill(t, append([]string{tc.cmd}, flags...), func(m *sandboxmsb.MockMsbClient) {
					m.SetGotSandbox(&sandboxmsb.MockSandboxHandle{})
				})

				assertInfoHasPrefix(t, ui, tc.infoPrefix)
			})
		}
	}

	for _, tc := range []struct {
		cmd        string
		spinner    string
		infoPrefix string
	}{
		{cmdStop, "Stopping project VM", "stopped project VM: "},
		{cmdKill, "Force-killing project VM", "killed project VM: "},
	} {
		t.Run(tc.cmd+" with --force", func(t *testing.T) {
			initTestRepo(t)
			ui := runStopKill(t, []string{tc.cmd, "--force"}, func(m *sandboxmsb.MockMsbClient) {
				m.SetGotSandbox(&sandboxmsb.MockSandboxHandle{})
			})

			assertSpinnerHas(t, ui, tc.spinner)
			assertInfoHasPrefix(t, ui, tc.infoPrefix)
		})

		t.Run(tc.cmd+" with -f", func(t *testing.T) {
			initTestRepo(t)
			ui := runStopKill(t, []string{tc.cmd, "-f"}, func(m *sandboxmsb.MockMsbClient) {
				m.SetGotSandbox(&sandboxmsb.MockSandboxHandle{})
			})

			assertInfoHasPrefix(t, ui, tc.infoPrefix)
		})
	}

	for _, cmd := range []string{cmdStop, cmdKill} {
		t.Run(cmd+" --force removes persisted state", func(t *testing.T) {
			initTestRepo(t)
			ui := runStopKill(t, []string{cmd, "--force"}, func(m *sandboxmsb.MockMsbClient) {
				m.SetGotSandbox(&sandboxmsb.MockSandboxHandle{})
			})

			assertVerboseHasPrefix(t, ui, "persisted state removed: ")
		})
	}

	for _, tc := range []struct {
		cmd        string
		infoPrefix string
	}{
		{cmdStop, "dry-run: Would stop"},
		{cmdKill, "dry-run: Would kill "},
	} {
		for _, flags := range stopKillFlags {
			t.Run("dry-run "+tc.cmd+" ignores state removal failure "+strings.Join(flags, " "), func(t *testing.T) {
				initTestRepo(t)
				ui := runStopKill(t, append([]string{tc.cmd}, flags...), func(m *sandboxmsb.MockMsbClient) {
					m.SetGotSandbox(&sandboxmsb.MockSandboxHandle{RemoveErr: errBoom})
				})

				assertInfoHasPrefix(t, ui, tc.infoPrefix)
				assertNoWarn(t, ui)
			})
		}
	}

	for _, cmd := range []string{cmdStop, cmdKill} {
		t.Run(cmd+" --force warns on state removal failure", func(t *testing.T) {
			initTestRepo(t)
			ui := runStopKill(t, []string{cmd, "--force"}, func(m *sandboxmsb.MockMsbClient) {
				m.SetGotSandbox(&sandboxmsb.MockSandboxHandle{RemoveErr: errBoom})
			})

			assertWarnContains(t, ui, "failed to remove sandbox state")
		})
	}
}

// TestStopKillAlreadyStopped covers stop/kill on an already-stopped VM: the
// action and persisted-state removal must be skipped, and the user told the VM
// is already stopped.
func TestStopKillAlreadyStopped(t *testing.T) {
	for _, tc := range []struct {
		cmd        string
		infoPrefix string
		action     func(h *sandboxmsb.MockSandboxHandle) bool
	}{
		{cmdStop, "project VM already stopped: ", func(h *sandboxmsb.MockSandboxHandle) bool { return h.DidStop }},
		{cmdKill, "project VM already killed: ", func(h *sandboxmsb.MockSandboxHandle) bool { return h.DidKill }},
	} {
		for _, flags := range stopKillFlags {
			t.Run(tc.cmd+" "+strings.Join(flags, " "), func(t *testing.T) {
				initTestRepo(t)
				handle := &sandboxmsb.MockSandboxHandle{Status_: msb.SandboxStatusStopped}
				ui := runStopKill(t, append([]string{tc.cmd}, flags...), func(m *sandboxmsb.MockMsbClient) {
					m.SetGotSandbox(handle)
				})

				assertInfoHasPrefix(t, ui, tc.infoPrefix)
				if tc.action(handle) {
					t.Errorf("expected no stop/kill action on already-stopped VM; got action")
				}
				if handle.DidRemove() {
					t.Errorf("expected no state removal on already-stopped VM; got removal")
				}
			})
		}
	}
}

// assertErrContains reports whether err is non-nil and contains wantPart.
func assertErrContains(t *testing.T, err error, wantPart string) {
	t.Helper()
	if err == nil {
		t.Errorf("expected error containing %q, got none", wantPart)
		return
	}
	if !strings.Contains(err.Error(), wantPart) {
		t.Errorf("expected error containing %q, got: %v", wantPart, err)
	}
}

// TestStopKillGetSandboxError covers a non-not-found GetSandbox failure, which
// is distinct from the "no project VM found" nil-return path.
func TestStopKillGetSandboxError(t *testing.T) {
	for _, tc := range []struct {
		cmd     string
		wantErr string
	}{
		{cmdStop, "get sandbox"},
		{cmdKill, "get sandbox"},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			initTestRepo(t)
			cmd, _ := setupStopKillConfig(t, []string{tc.cmd}, func(m *sandboxmsb.MockMsbClient) {
				m.SetGetSandboxErr(errBoom)
			})
			assertErrContains(t, cmd.Execute(), tc.wantErr)
		})
	}
}

// TestStopKillActionError covers the Stop/Kill call itself failing.
func TestStopKillActionError(t *testing.T) {
	for _, tc := range []struct {
		cmd     string
		sbErr   func(*sandboxmsb.MockSandboxHandle)
		wantErr string
	}{
		{cmdStop, func(h *sandboxmsb.MockSandboxHandle) { h.StopErr = errBoom }, "stop sandbox"},
		{cmdKill, func(h *sandboxmsb.MockSandboxHandle) { h.KillErr = errBoom }, "kill sandbox"},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			initTestRepo(t)
			cmd, _ := setupStopKillConfig(t, []string{tc.cmd}, func(m *sandboxmsb.MockMsbClient) {
				handle := &sandboxmsb.MockSandboxHandle{}
				tc.sbErr(handle)
				m.SetGotSandbox(handle)
			})
			assertErrContains(t, cmd.Execute(), tc.wantErr)
		})
	}
}

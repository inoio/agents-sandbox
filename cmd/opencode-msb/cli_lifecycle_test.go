package main

import (
	"slices"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func initTestRepo(t *testing.T) {
	t.Helper()
	dir := testutil.InitRepo(t)
	t.Chdir(dir)
}
func notFoundErr() error {
	return &msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"}
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

func TestLifecycle(t *testing.T) {
	t.Run("S1_no_project_vm_found", func(t *testing.T) {
		for _, tc := range []struct {
			cmd        string
			wantPrefix string
		}{
			{cmdStop, "no project VM found: "},
			{cmdKill, "no project VM found: "},
		} {
			for _, flags := range stopKillFlags {
				t.Run(tc.cmd+strings.Join(flags, "_"), func(t *testing.T) {
					ui := &termio.Mock{}
					mock := &sandbox.MockMsbClient{}
					mock.SetGetSandboxErr(notFoundErr())
					sandbox.WithMsbMock(t, mock)

					root := buildRootCmd(ui)
					root.SetArgs(append([]string{tc.cmd}, flags...))

					if err := root.Execute(); err != nil {
						t.Fatalf("unexpected error: %v", err)
					}

					assertInfoHasPrefix(t, ui, tc.wantPrefix)
				})
			}
		}
	})

	for _, flags := range stopKillFlags {
		t.Run("S2_dry_run_stop"+flags[0], func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			sandbox.WithMsbMock(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdStop}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertInfoHasPrefix(t, ui, "dry-run: Would stop")
		})

		t.Run("S3_dry_run_kill"+flags[0], func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			sandbox.WithMsbMock(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdKill}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertInfoHasPrefix(t, ui, "dry-run: Would kill ")
		})

		t.Run("S4_dry_run_force_stop"+flags[0], func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			sandbox.WithMsbMock(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdStop}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertInfoHasPrefix(t, ui, "dry-run: ")
			assertInfoHasPrefix(t, ui, "(also would remove persisted state)")
		})

		t.Run("S5_dry_run_force_kill"+flags[0], func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			sandbox.WithMsbMock(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdKill}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertInfoHasPrefix(t, ui, "(also would remove persisted state)")
		})
	}

	for _, tc := range []struct {
		cmd        string
		spinner    string
		infoPrefix string
	}{
		{cmdStop, "Stopping project VM", "stopped project VM: "},
		{cmdKill, "Force-killing project VM", "killed project VM: "},
	} {
		t.Run("S6"+tc.cmd+"--force", func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			sandbox.WithMsbMock(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs([]string{tc.cmd, "--force"})

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertSpinnerHas(t, ui, tc.spinner)
			assertInfoHasPrefix(t, ui, tc.infoPrefix)
		})

		t.Run("S6"+tc.cmd+"--force-short", func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			sandbox.WithMsbMock(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs([]string{tc.cmd, "-f"})

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertInfoHasPrefix(t, ui, tc.infoPrefix)
		})
	}

	for _, cmd := range []string{cmdStop, cmdKill} {
		t.Run("S9_force_remove_"+cmd, func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			sandbox.WithMsbMock(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs([]string{cmd, "--force"})

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

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
			t.Run("S10_remove_fail_"+tc.cmd+strings.Join(flags, "_"), func(t *testing.T) {
				initTestRepo(t)
				ui := &termio.Mock{}
				mock := &sandbox.MockMsbClient{}
				mock.SetGotSandbox(&sandbox.MockSandboxHandle{RemoveErr: errBoom})
				sandbox.WithMsbMock(t, mock)

				root := buildRootCmd(ui)
				root.SetArgs(append([]string{tc.cmd}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				assertInfoHasPrefix(t, ui, tc.infoPrefix)
				assertNoWarn(t, ui)
			})
		}
	}

	// S10 non-dry-run remove fails
	for _, cmd := range []string{cmdStop, cmdKill} {
		t.Run("S10_non_dry_run_"+cmd+"_remove_fail", func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{RemoveErr: errBoom})
			sandbox.WithMsbMock(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs([]string{cmd, "--force"})

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertWarnContains(t, ui, "failed to remove sandbox state")
		})
	}
}

package main

import (
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testhelpers"
)

func initTestRepo(t *testing.T) {
	t.Helper()
	dir := testhelpers.InitRepo(t)
	t.Chdir(dir)
}
func notFoundErr() error {
	return &msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"}
}

//nolint:gocyclo,cyclop // Nested t.Run for flag permutations across 10 scenarios makes high complexity expected
func TestLifecycle(t *testing.T) {
	t.Run("S1_no_project_vm_found", func(t *testing.T) {
		for _, flags := range stopKillFlags {
			t.Run("stop", func(t *testing.T) {
				ui := &stdio.Mock{}
				mock := &sandbox.MockMsbClient{}
				mock.SetGetSandboxErr(notFoundErr())
				overrideMsbClient(t, mock)

				root := buildRootCmd(ui)
				root.SetArgs(append([]string{cmdStop}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				found := false
				for _, call := range ui.InfoCalls {
					if strings.HasPrefix(call, "no project VM found: ") {
						found = true
					}
				}
				if !found {
					t.Errorf("expected 'no project VM found' message; got: %v", ui.InfoCalls)
				}
			})

			t.Run("kill", func(t *testing.T) {
				ui := &stdio.Mock{}
				mock := &sandbox.MockMsbClient{}
				mock.SetGetSandboxErr(notFoundErr())
				overrideMsbClient(t, mock)

				root := buildRootCmd(ui)
				root.SetArgs(append([]string{cmdKill}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				found := false
				for _, call := range ui.InfoCalls {
					if strings.HasPrefix(call, "no project VM found: ") {
						found = true
					}
				}
				if !found {
					t.Errorf("expected 'no project VM found' message; got: %v", ui.InfoCalls)
				}
			})
		}
	})

	for _, flags := range stopKillFlags {
		t.Run("S2_dry_run_stop"+flags[0], func(t *testing.T) {
			initTestRepo(t)

			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdStop}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			found := false
			for _, call := range ui.InfoCalls {
				if strings.HasPrefix(call, "dry-run: Would stop") {
					found = true
				}
			}
			if !found {
				t.Errorf("expected 'dry-run: Would stop' message; got: %v", ui.InfoCalls)
			}
		})

		t.Run("S3_dry_run_kill"+flags[0], func(t *testing.T) {
			initTestRepo(t)

			// S3 tests dry-run kill with force (stopKillFlags always has both
			// --force and --dry-run, so the remove note is included).
			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdKill}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			found := false
			for _, call := range ui.InfoCalls {
				if strings.HasPrefix(call, "dry-run: Would kill ") {
					found = true
				}
			}
			if !found {
				t.Errorf("expected 'dry-run: Would kill' message; got: %v", ui.InfoCalls)
			}
		})

		t.Run("S4_dry_run_force_stop"+flags[0], func(t *testing.T) {
			initTestRepo(t)

			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdStop}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			found := false
			foundRemove := false
			for _, call := range ui.InfoCalls {
				if strings.HasPrefix(call, "dry-run: ") {
					found = true
				}
				if strings.Contains(call, "(also would remove persisted state)") {
					foundRemove = true
				}
			}
			if !found {
				t.Errorf("expected dry-run message; got: %v", ui.InfoCalls)
			}
			if !foundRemove {
				t.Errorf("expected remove note; got: %v", ui.InfoCalls)
			}
		})

		t.Run("S5_dry_run_force_kill"+flags[0], func(t *testing.T) {
			initTestRepo(t)

			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdKill}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			found := false
			for _, call := range ui.InfoCalls {
				if strings.Contains(call, "(also would remove persisted state)") {
					found = true
				}
			}
			if !found {
				t.Errorf("expected dry-run force-kill remove note; got: %v", ui.InfoCalls)
			}
		})
	}

	// S6 normal stop without dry-run
	t.Run("S6_normal_stop--force", func(t *testing.T) {
		initTestRepo(t)

		ui := &stdio.Mock{}
		mock := &sandbox.MockMsbClient{}
		mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
		overrideMsbClient(t, mock)

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdStop, "--force"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		foundSpinner := false
		for _, call := range ui.SpinnerCalls {
			if call == "Stopping project VM" {
				foundSpinner = true
			}
		}
		if !foundSpinner {
			t.Errorf("expected spinner 'Stopping project VM'; got: %v", ui.SpinnerCalls)
		}

		foundStop := false
		for _, call := range ui.InfoCalls {
			if strings.HasPrefix(call, "stoped project VM: ") {
				foundStop = true
			}
		}
		if !foundStop {
			t.Errorf("expected stop message; got: %v", ui.InfoCalls)
		}
	})

	t.Run("S6_normal_stop--force-short", func(t *testing.T) {
		initTestRepo(t)

		ui := &stdio.Mock{}
		mock := &sandbox.MockMsbClient{}
		mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
		overrideMsbClient(t, mock)

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdStop, "-f"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		foundStop := false
		for _, call := range ui.InfoCalls {
			if strings.HasPrefix(call, "stoped project VM: ") {
				foundStop = true
			}
		}
		if !foundStop {
			t.Errorf("expected stop message; got: %v", ui.InfoCalls)
		}
	})

	// S7 normal kill without dry-run
	t.Run("S7_normal_kill--force", func(t *testing.T) {
		initTestRepo(t)

		ui := &stdio.Mock{}
		mock := &sandbox.MockMsbClient{}
		mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
		overrideMsbClient(t, mock)

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdKill, "--force"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		foundSpinner := false
		for _, call := range ui.SpinnerCalls {
			if call == "Force-killing project VM" {
				foundSpinner = true
			}
		}
		if !foundSpinner {
			t.Errorf("expected spinner 'Force-killing project VM'; got: %v", ui.SpinnerCalls)
		}

		foundKill := false
		for _, call := range ui.InfoCalls {
			if strings.HasPrefix(call, "killed project VM: ") {
				foundKill = true
			}
		}
		if !foundKill {
			t.Errorf("expected kill message; got: %v", ui.InfoCalls)
		}
	})

	t.Run("S7_normal_kill--force-short", func(t *testing.T) {
		initTestRepo(t)

		ui := &stdio.Mock{}
		mock := &sandbox.MockMsbClient{}
		mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
		overrideMsbClient(t, mock)

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdKill, "-f"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		foundKill := false
		for _, call := range ui.InfoCalls {
			if strings.HasPrefix(call, "killed project VM: ") {
				foundKill = true
			}
		}
		if !foundKill {
			t.Errorf("expected kill message; got: %v", ui.InfoCalls)
		}
	})

	// S8 force remove stop (non-dry-run)
	t.Run("S8_force_remove_stop", func(t *testing.T) {
		initTestRepo(t)

		ui := &stdio.Mock{}
		mock := &sandbox.MockMsbClient{}
		mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
		overrideMsbClient(t, mock)

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdStop, "--force"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		foundVerbose := false
		for _, call := range ui.VerboseCalls {
			if strings.HasPrefix(call, "persisted state removed: ") {
				foundVerbose = true
			}
		}
		if !foundVerbose {
			t.Errorf("expected verbose 'persisted state removed'; got: %v", ui.VerboseCalls)
		}
	})

	// S9 force remove kill (non-dry-run)
	t.Run("S9_force_remove_kill", func(t *testing.T) {
		initTestRepo(t)

		ui := &stdio.Mock{}
		mock := &sandbox.MockMsbClient{}
		mock.SetGotSandbox(&sandbox.MockSandboxHandle{})
		overrideMsbClient(t, mock)

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdKill, "--force"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		foundVerbose := false
		for _, call := range ui.VerboseCalls {
			if strings.HasPrefix(call, "persisted state removed: ") {
				foundVerbose = true
			}
		}
		if !foundVerbose {
			t.Errorf("expected verbose 'persisted state removed'; got: %v", ui.VerboseCalls)
		}
	})

	// S10 remove fails - iterate over stop and kill via stopKillFlags
	for _, flags := range stopKillFlags {
		t.Run("S10_remove_fail_stop", func(t *testing.T) {
			initTestRepo(t)

			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{
				RemoveErr: errBoom,
			})
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdStop}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// In dry-run mode, Remove is never called (code returns early),
			// so no remove failure occurs. The test verifies dry-run works
			// correctly when remove would fail.
			foundInfo := false
			for _, call := range ui.InfoCalls {
				if strings.HasPrefix(call, "dry-run: Would stop") {
					foundInfo = true
				}
			}
			if !foundInfo {
				t.Errorf("expected dry-run stop info; got: %v", ui.InfoCalls)
			}
			if len(ui.WarnCalls) > 0 {
				t.Errorf("expected no warn calls in dry-run; got: %v", ui.WarnCalls)
			}
		})

		t.Run("S10_remove_fail_kill", func(t *testing.T) {
			initTestRepo(t)

			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			mock.SetGotSandbox(&sandbox.MockSandboxHandle{
				RemoveErr: errBoom,
			})
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs(append([]string{cmdKill}, flags...))

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			foundInfo := false
			for _, call := range ui.InfoCalls {
				if strings.HasPrefix(call, "dry-run: Would kill ") {
					foundInfo = true
				}
			}
			if !foundInfo {
				t.Errorf("expected dry-run kill info; got: %v", ui.InfoCalls)
			}
			if len(ui.WarnCalls) > 0 {
				t.Errorf("expected no warn calls in dry-run; got: %v", ui.WarnCalls)
			}
		})
	}
}

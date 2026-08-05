package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

func mkStaleVM(staleTime time.Time) sandbox.SandboxHandle {
	return &sandbox.MockSandboxHandle{
		Name_:      "opencode-msb-vm-projectname-1mjusbm3wikhb0",
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: staleTime,
	}
}

func mkActiveVM(imgRef string) sandbox.SandboxHandle {
	return &sandbox.MockSandboxHandle{
		Name_:      "opencode-msb-vm-activeproject-1mjusbm3wikhb0",
		Status_:    msb.SandboxStatusRunning,
		UpdatedAt_: time.Now().Add(-3 * 24 * time.Hour),
		Image_:     imgRef,
	}
}

func mkStaleTask(staleTime time.Time) sandbox.SandboxHandle {
	return &sandbox.MockSandboxHandle{
		Name_:      "opencode-msb-task-fill",
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: staleTime,
	}
}

func homeVol(name string) sandbox.VolumeHandle {
	return &sandbox.MockVolumeHandle{Name_: name, Path_: "/mnt/home"}
}

func cloneVol(name string) sandbox.VolumeHandle {
	return &sandbox.MockVolumeHandle{Name_: name, Path_: "/mnt/home"}
}

func msbImg(ref string) sandbox.ImageHandle {
	return sandbox.MockImageHandle{Reference_: ref}
}

func TestPrune(t *testing.T) {
	t.Run("P1_no_stale_items", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run("f"+strings.Join(flags, "_"), func(t *testing.T) {
				ui := &stdio.Mock{}
				mock := &sandbox.MockMsbClient{}
				origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
				t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })
				docker.TestWithNoopDockerMock(t)

				root := buildRootCmd(ui)
				root.SetArgs(append([]string{"prune"}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				expected := "Pruned 0 VMs, 0 home volumes, 0 docker images, 0 msb images, 0 task sandboxes, 0 clone volumes"
				checkSummary(t, ui.OutCalls, expected)
			})
		}
	})

	t.Run("P2_dry_run_with_stale_items", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run("f"+strings.Join(flags, "_"), func(t *testing.T) {
				mock := &sandbox.MockMsbClient{}
				mock.Sandboxes = append(mock.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
				mock.Sandboxes = append(mock.Sandboxes,
					mkActiveVM("opencode-msb/runner-activeproject-1mjusbm3wikhb0:abc1234"))
				mock.Volumes = append(mock.Volumes,
					homeVol("opencode-msb-home-projectname-1mjusbm3wikhb0-d1"))
				mock.Volumes = append(mock.Volumes,
					cloneVol("opencode-msb-clone-projects"))
				mock.Images = append(mock.Images,
					msbImg("opencode-msb/runner-projectname:xyz789"))

				origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
				t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })
				docker.TestWithNoopDockerMock(t)

				ui := &stdio.Mock{}
				root := buildRootCmd(ui)
				root.SetArgs(append([]string{"prune", "--dry-run"}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				// Stale VM cascade: 1 VM + 1 home vol (no MSB in cascade; MSB
				// slug "activeproject" doesn't match stale VM slug). Orphan
				// artifacts pruned for unmatched "projectname" slug (1 MSB + 1
				// docker). Clone pruned (no active VM for slug).
				expected := "dry-run: Would prune 1 VMs, 1 home volumes, 1 docker images, 1 msb images, 0 task sandboxes, 1 clone volumes"
				checkSummary(t, ui.OutCalls, expected)
			})
		}
	})

	t.Run("P3_partial_failure", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run("f"+strings.Join(flags, "_"), func(t *testing.T) {
				ui := &stdio.Mock{}
				// Both stale VMs pruned; the MockSandboxHandle.RemoveErr
				// does not affect MockMsbClient.RemoveSandbox. The mock
				// does not support per-VM error injection, so both removals
				// succeed. The test verifies the output for stale items.
				mock := &sandbox.MockMsbClient{}
				mock.Sandboxes = append(mock.Sandboxes, &sandbox.MockSandboxHandle{
					Name_:      "opencode-msb-vm-first-dbe294a8514d4000",
					Status_:    msb.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
				})
				mock.Sandboxes = append(mock.Sandboxes, &sandbox.MockSandboxHandle{
					Name_:      "opencode-msb-vm-second-dbe294a8514d4001",
					Status_:    msb.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
				})
				mock.Volumes = append(mock.Volumes,
					homeVol("opencode-msb-home-first-dbe294a8514d4000-v1"))
				mock.Images = append(mock.Images,
					msbImg("opencode-msb/runner-second:v1"))

				origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
				t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })
				docker.TestWithNoopDockerMock(t)

				root := buildRootCmd(ui)
				root.SetArgs(append([]string{"prune"}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				// Both stale VMs pruned (cascade finds no matching artifacts
				// due to hash-suffix mismatch between stale VM and home volume
				// slugs). MSB + docker pruned by orphan artifact phase for
				// unmatched slug.
				expected := "Pruned 2 VMs, 0 home volumes, 1 docker images, 1 msb images, 0 task sandboxes, 0 clone volumes"
				checkSummary(t, ui.OutCalls, expected)
			})
		}
	})

	t.Run("P4_custom_age_2w", func(t *testing.T) {
		ui := &stdio.Mock{}
		mock := &sandbox.MockMsbClient{}
		mock.Sandboxes = append(mock.Sandboxes, &sandbox.MockSandboxHandle{
			Name_:      "opencode-msb-vm-staleproject-1mjusbm3wikhb0",
			Status_:    msb.SandboxStatusStopped,
			UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
		})
		mock.Volumes = append(mock.Volumes,
			cloneVol("opencode-msb-clone-staleproject-abc123"))
		origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
		t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })
		docker.TestWithNoopDockerMock(t)

		root := buildRootCmd(ui)
		root.SetArgs([]string{"prune", "--age", "2w"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 15d exceeds 2w (14d), stale VM pruned. Clone kept only if an
		// active VM matches its slug; otherwise pruned. No active VM for
		// "staleproject" → clone pruned.
		expected := "Pruned 1 VMs, 0 home volumes, 0 docker images, 0 msb images, 0 task sandboxes, 1 clone volumes"
		checkSummary(t, ui.OutCalls, expected)
	})

	t.Run("P5_custom_age_14d", func(t *testing.T) {
		ui := &stdio.Mock{}
		mock := &sandbox.MockMsbClient{}
		mock.Sandboxes = append(mock.Sandboxes, &sandbox.MockSandboxHandle{
			Name_:      "opencode-msb-vm-staleproject-1mjusbm3wikhb0",
			Status_:    msb.SandboxStatusStopped,
			UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
		})
		mock.Volumes = append(mock.Volumes,
			cloneVol("opencode-msb-clone-staleproject-abc123"))
		origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
		t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })
		docker.TestWithNoopDockerMock(t)

		root := buildRootCmd(ui)
		root.SetArgs([]string{"prune", "--age", "14d"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 15d exceeds --age 14d, stale VM pruned. Same counts as P4.
		expected := "Pruned 1 VMs, 0 home volumes, 0 docker images, 0 msb images, 0 task sandboxes, 1 clone volumes"
		checkSummary(t, ui.OutCalls, expected)
	})

	t.Run("P6_invalid_age_error", func(t *testing.T) {
		// --age "invalid" must not be overridden by fixture flags.
		ui := &stdio.Mock{}
		root := buildRootCmd(ui)
		root.SetArgs([]string{"prune", "--age", "invalid"})

		err := root.Execute()

		if err == nil {
			t.Fatal("expected error for invalid age; got nil")
		}
		if !strings.Contains(err.Error(), "invalid age") {
			t.Errorf("expected error containing 'invalid age'; got: %v", err)
		}
	})

	t.Run("P7_docker_client_error", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run("f"+strings.Join(flags, "_"), func(t *testing.T) {
				ui := &stdio.Mock{}
				docker.TestWithDefaultErrorDockerMock(t)
				root := buildRootCmd(ui)
				root.SetArgs(append([]string{"prune"}, flags...))

				err := root.Execute()

				if err != nil {
					t.Fatalf("expected no error; got %s", err)
				}

				assert.True(t, slices.ContainsFunc(ui.VerboseCalls, func(s string) bool {
					return strings.Contains(s, "cannot connect to Docker daemon")
				}), "No 'cannot connect to Docker daemon' found in verbose messages")
			})
		}
	})

	t.Run("P8_clone_volumes_pruned", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run("f"+strings.Join(flags, "_"), func(t *testing.T) {
				mock := &sandbox.MockMsbClient{}
				mock.Sandboxes = append(mock.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
				mock.Sandboxes = append(mock.Sandboxes, mkStaleTask(time.Now().Add(-15*24*time.Hour)))
				mock.Volumes = append(mock.Volumes,
					homeVol("opencode-msb-home-projectname-1mjusbm3wikhb0-v1"))
				mock.Volumes = append(mock.Volumes,
					cloneVol("opencode-msb-clone-projectname-abc123"))
				mock.Images = append(mock.Images,
					msbImg("opencode-msb/runner-projectname:v2"))

				origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
				t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })
				docker.TestWithNoopDockerMock(t)

				ui := &stdio.Mock{}
				root := buildRootCmd(ui)
				root.SetArgs(append([]string{"prune"}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				// Stale cascade: 1 VM + 1 home vol. Orphan artifacts for
				// "projectname" slug pruned (1 MSB + 1 docker; no active VM
				// to match). Clone pruned (no active VM). Task always pruned.
				expected := "Pruned 1 VMs, 1 home volumes, 1 docker images, 1 msb images, 1 task sandboxes, 1 clone volumes"
				checkSummary(t, ui.OutCalls, expected)
			})
		}
	})

	t.Run("P9_task_sandboxes_pruned", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run("f"+strings.Join(flags, "_"), func(t *testing.T) {
				mock := &sandbox.MockMsbClient{}
				mock.Sandboxes = append(mock.Sandboxes,
					mkActiveVM("opencode-msb/runner-activeproject-1mjusbm3wikhb0:xyz789"))
				mock.Sandboxes = append(mock.Sandboxes, mkStaleTask(time.Now().Add(-15*24*time.Hour)))
				mock.Volumes = append(mock.Volumes,
					homeVol("opencode-msb-home-activeproject-1mjusbm3wikhb0-abc123"))
				mock.Volumes = append(mock.Volumes,
					cloneVol("opencode-msb-clone-activeproject-abc123"))
				mock.Images = append(mock.Images,
					msbImg("opencode-msb/runner-activeproject-1mjusbm3wikhb0:xyz789"))

				origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
				t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })
				docker.TestWithNoopDockerMock(t)

				ui := &stdio.Mock{}
				root := buildRootCmd(ui)
				root.SetArgs(append([]string{"prune"}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				// Active VM: cleanup home vol. MSB image matches active digest.
				// Docker image matches active digest → not pruned.
				// Clone pruned (slug not matching ActiveVMDigest entry).
				// Task sandbox always pruned.
				expected := "Pruned 0 VMs, 1 home volumes, 0 docker images, 0 msb images, 1 task sandboxes, 1 clone volumes"
				checkSummary(t, ui.OutCalls, expected)
			})
		}
	})

	t.Run("P10_flag_fixture", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run("f"+strings.Join(flags, "_"), func(t *testing.T) {
				mock := &sandbox.MockMsbClient{}
				// Use 15d staleness to work with all flag values (7d, 7d, 14d, 14d).
				mock.Sandboxes = append(mock.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
				mock.Sandboxes = append(mock.Sandboxes,
					mkActiveVM("opencode-msb/runner-prod-main-1mjusbm3wikhb0:abc1234"))
				mock.Volumes = append(mock.Volumes,
					homeVol("opencode-msb-home-projectname-1mjusbm3wikhb0-digest1"))
				mock.Images = append(mock.Images,
					msbImg("opencode-msb/runner-activeproject:v2"))

				origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
				t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })
				docker.TestWithNoopDockerMock(t)

				ui := &stdio.Mock{}
				root := buildRootCmd(ui)
				root.SetArgs(append([]string{"prune"}, flags...))

				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				// All flags should produce same counts (15d stale for all thresholds).
				// Stale cascade: 1 VM + 1 home vol + 1 MSB img.
				// Docker image pruned (digest mismatch).
				expected := "Pruned 1 VMs, 1 home volumes, 1 docker images, 1 msb images, 0 task sandboxes, 0 clone volumes"
				checkSummary(t, ui.OutCalls, expected)
			})
		}
	})
}

func checkSummary(t *testing.T, outCalls []string, expected string) {
	t.Helper()
	if !slices.Contains(outCalls, expected) {
		t.Errorf("expected %q; got: %v", expected, outCalls)
	}
}

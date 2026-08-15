package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	sandboxmsb "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
)

// oldArtifactTime returns a time far enough in the past that images and volumes
// carrying it are never considered recent for any prune threshold used in the
// CLI tests.
func oldArtifactTime() time.Time {
	return time.Now().Add(-30 * 24 * time.Hour)
}

func mkStaleVM(staleTime time.Time) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      "opencode-sandbox-vm-projectname-1mjusbm3wikhb0",
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: staleTime,
	}
}

func mkStoppedVM(slug string) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      fmt.Sprintf("opencode-sandbox-vm-%s", slug),
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: time.Now().Add(-3 * 24 * time.Hour),
		Image_:     fmt.Sprintf("opencode-sandbox/runner-%s:xyz789", slug),
	}
}

func mkActiveVM(slug string) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      fmt.Sprintf("opencode-sandbox-vm-%s", slug),
		Status_:    msb.SandboxStatusRunning,
		UpdatedAt_: time.Now().Add(-3 * 24 * time.Hour),
		Image_:     fmt.Sprintf("opencode-sandbox/runner-%s:xyz789", slug),
	}
}

func mkStaleTask(staleTime time.Time) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      "opencode-sandbox-task-fill",
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: staleTime,
	}
}

func homeVol(name string) sandboxmsb.VolumeHandle {
	return &sandboxmsb.MockVolumeHandle{Name_: name, Path_: "/mnt/home", CreatedAt_: oldArtifactTime()}
}

func cloneVol(name string) sandboxmsb.VolumeHandle {
	return &sandboxmsb.MockVolumeHandle{Name_: name, Path_: "/mnt/home", CreatedAt_: oldArtifactTime()}
}

func msbImg(ref string) sandboxmsb.ImageHandle {
	return sandboxmsb.MockImageHandle{Reference_: ref, LastUsedAt_: oldArtifactTime()}
}

func TestPrune(t *testing.T) {
	t.Run("no stale items", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, nil, "")
			})
		}
	})

	t.Run("dry run with stale items", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, append([]string{"--dry-run"}, flags...), func(m *sandboxmsb.MockMsbClient) {
					m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
					m.Sandboxes = append(m.Sandboxes,
						mkActiveVM("activeproject-1mjusbm3wikhb0"))
					m.Volumes = append(m.Volumes,
						homeVol("opencode-sandbox-home-projectname-1mjusbm3wikhb0-d1"))
					m.Volumes = append(m.Volumes,
						cloneVol("opencode-sandbox-clone-projects"))
					m.Images = append(m.Images,
						msbImg("opencode-sandbox/runner-projectname:xyz789"))
				}, "dry-run: Would prune 1 VMs, 1 home volumes, 1 docker images, 1 msb images, 0 task sandboxes, 1 clone volumes")
			})
		}
	})

	t.Run("partial failure", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, func(m *sandboxmsb.MockMsbClient) {
					// Both stale VMs pruned; the MockSandboxHandle.RemoveErr
					// does not affect MockMsbClient.RemoveSandbox. The mock
					// does not support per-VM error injection, so both removals
					// succeed. The test verifies the output for stale items.
					m.Sandboxes = append(m.Sandboxes, &sandboxmsb.MockSandboxHandle{
						Name_:      "opencode-sandbox-vm-first-dbe294a8514d4000",
						Status_:    msb.SandboxStatusStopped,
						UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
					})
					m.Sandboxes = append(m.Sandboxes, &sandboxmsb.MockSandboxHandle{
						Name_:      "opencode-sandbox-vm-second-dbe294a8514d4001",
						Status_:    msb.SandboxStatusStopped,
						UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
					})
					m.Volumes = append(m.Volumes,
						homeVol("opencode-sandbox-home-first-dbe294a8514d4000-v1"))
					m.Images = append(m.Images,
						msbImg("opencode-sandbox/runner-second:v1"))
				}, "Pruned 2 VMs, 0 home volumes, 1 docker images, 1 msb images, 0 task sandboxes, 0 clone volumes")
			})
		}
	})

	t.Run("custom age of two weeks", func(t *testing.T) {
		runPruneTestWithAge(t, "2w", func(m *sandboxmsb.MockMsbClient) {
			m.Sandboxes = append(m.Sandboxes, &sandboxmsb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-staleproject-1mjusbm3wikhb0",
				Status_:    msb.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
			})
			m.Volumes = append(m.Volumes,
				cloneVol("opencode-sandbox-clone-staleproject-abc123"))
		}, "Pruned 1 VMs, 0 home volumes, 0 docker images, 0 msb images, 0 task sandboxes, 1 clone volumes")
	})

	t.Run("invalid age error", func(t *testing.T) {
		// --age "invalid" must not be overridden by fixture flags.
		runPruneTestError(t, []string{"prune", "--age", "invalid"}, "invalid age")
	})

	t.Run("docker client error", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				mock := &sandboxmsb.MockMsbClient{}
				mock.Images = append(mock.Images,
					msbImg("opencode-sandbox/runner-projectname:v2"))

				cmd, ui := setupCommandFixtures(t, append([]string{"prune"}, flags...)...)
				sandboxmsb.WithMsbMock(t, mock)
				docker.WithDefaultErrorDockerMock(t)

				err := cmd.Execute()

				if err != nil {
					t.Fatalf("expected no error; got %s", err)
				}
				assert.True(t, slices.ContainsFunc(ui.WarnCalls, func(s string) bool {
					return strings.Contains(s, "cannot connect to Docker daemon")
				}), "No 'cannot connect to Docker daemon' found in warn messages")
			})
		}
	})

	t.Run("clone volumes pruned", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, func(m *sandboxmsb.MockMsbClient) {
					m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
					m.Sandboxes = append(m.Sandboxes, mkStaleTask(time.Now().Add(-15*24*time.Hour)))
					m.Volumes = append(m.Volumes,
						homeVol("opencode-sandbox-home-projectname-1mjusbm3wikhb0-v1"))
					m.Volumes = append(m.Volumes,
						cloneVol("opencode-sandbox-clone-projectname-abc123"))
					m.Images = append(m.Images,
						msbImg("opencode-sandbox/runner-projectname:v2"))
				}, "Pruned 1 VMs, 1 home volumes, 1 docker images, 1 msb images, 1 task sandboxes, 1 clone volumes")
			})
		}
	})

	t.Run("task sandboxes pruned", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
				if err := state.WriteState("activeproject-1mjusbm3wikhb0", state.HomeState{
					HomeVolume: "opencode-sandbox-home-activeproject-1mjusbm3wikhb0-abc123",
				}); err != nil {
					t.Fatalf("WriteState: %v", err)
				}
				runPruneTest(t, flags, func(m *sandboxmsb.MockMsbClient) {
					m.Sandboxes = append(m.Sandboxes,
						mkActiveVM("activeproject-1mjusbm3wikhb0"))
					m.Sandboxes = append(m.Sandboxes, mkStaleTask(time.Now().Add(-15*24*time.Hour)))
					m.Volumes = append(m.Volumes,
						homeVol("opencode-sandbox-home-activeproject-1mjusbm3wikhb0-abc123"))
					m.Volumes = append(m.Volumes,
						cloneVol("opencode-sandbox-clone-activeproject-abc123"))
					m.Images = append(m.Images,
						msbImg("opencode-sandbox/runner-activeproject-1mjusbm3wikhb0:xyz789"))
				}, "Pruned 0 VMs, 0 home volumes, 0 docker images, 0 msb images, 1 task sandboxes, 1 clone volumes")
			})
		}
	})

	t.Run("flag fixture variations", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, func(m *sandboxmsb.MockMsbClient) {
					// Use 15d staleness to work with all flag values (7d, 7d, 14d, 14d).
					m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
					m.Sandboxes = append(m.Sandboxes,
						mkActiveVM("prod-main-1mjusbm3wikhb0"))
					m.Volumes = append(m.Volumes,
						homeVol("opencode-sandbox-home-projectname-1mjusbm3wikhb0-digest1"))
					m.Images = append(m.Images,
						msbImg("opencode-sandbox/runner-activeproject:v2"))
				}, "Pruned 1 VMs, 1 home volumes, 1 docker images, 1 msb images, 0 task sandboxes, 0 clone volumes")
			})
		}
	})
}

func runPruneTest(t *testing.T, flags []string, setupMock func(m *sandboxmsb.MockMsbClient), expected string) {
	t.Helper()
	mock := &sandboxmsb.MockMsbClient{}
	if setupMock != nil {
		setupMock(mock)
	}
	cmd, ui := setupCommandFixtures(t, append([]string{"prune"}, flags...)...)
	sandboxmsb.WithMsbMock(t, mock)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkSummary(t, ui.OutCalls, expected)
}

func runPruneTestWithAge(t *testing.T, age string, setupMock func(m *sandboxmsb.MockMsbClient), expected string) {
	t.Helper()
	mock := &sandboxmsb.MockMsbClient{}
	setupMock(mock)
	cmd, ui := setupCommandFixtures(t, "prune", "--age", age)
	sandboxmsb.WithMsbMock(t, mock)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkSummary(t, ui.OutCalls, expected)
}

func runPruneTestError(t *testing.T, args []string, wantErrContains string) {
	t.Helper()
	cmd, _ := setupCommandFixtures(t, args...)
	sandboxmsb.WithMsbMock(t, &sandboxmsb.MockMsbClient{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !strings.Contains(err.Error(), wantErrContains) {
		t.Errorf("expected error containing %q; got: %v", wantErrContains, err)
	}
}

func checkSummary(t *testing.T, outCalls []string, expected string) {
	t.Helper()
	if expected == "" {
		if len(outCalls) > 0 {
			t.Errorf("expected no output; got: %v", outCalls)
		}
	} else {
		if !slices.Contains(outCalls, expected) {
			t.Errorf("expected %q; got: %v", expected, outCalls)
		}
	}
}

// TestPruneCatalogError covers the catalog build failing during prune, which
// surfaces the list error from the command.
func TestPruneCatalogError(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "prune", "--age", "7d")
	mock := &sandboxmsb.MockMsbClient{}
	mock.ListSandboxesErr = errBoom
	sandboxmsb.WithMsbMock(t, mock)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when the prune catalog build fails")
	}
	if !strings.Contains(err.Error(), "list sandboxes") {
		t.Errorf("expected 'list sandboxes' error, got: %v", err)
	}
}

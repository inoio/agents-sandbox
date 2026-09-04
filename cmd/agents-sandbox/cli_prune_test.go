package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// oldArtifactTime returns a time far enough in the past that images and volumes
// carrying it are never considered recent for any prune threshold used in the
// CLI tests.
func oldArtifactTime() time.Time {
	return time.Now().Add(-30 * 24 * time.Hour)
}

func mkStaleVM(staleTime time.Time) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-projectname-1mjusbm3wikhb0",
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: staleTime,
	}
}

func mkStoppedVM(slug string) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      fmt.Sprintf("agents-sandbox-vm-%s", slug),
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: time.Now().Add(-3 * 24 * time.Hour),
		Image_:     fmt.Sprintf("agents-sandbox/runner-%s:xyz789", slug),
	}
}

func mkActiveVM(slug string) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      fmt.Sprintf("agents-sandbox-vm-%s", slug),
		Status_:    msb.SandboxStatusRunning,
		UpdatedAt_: time.Now().Add(-3 * 24 * time.Hour),
		Image_:     fmt.Sprintf("agents-sandbox/runner-%s:xyz789", slug),
	}
}

func mkStaleTask(staleTime time.Time) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      "agents-sandbox-task-fill",
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: staleTime,
	}
}

func homeVol(name string) sandboxmsb.VolumeHandle {
	return &sandboxmsb.MockVolumeHandle{Name_: name, Path_: "/mnt/home", CreatedAt_: oldArtifactTime()}
}

func msbImg(ref string) sandboxmsb.ImageHandle {
	return sandboxmsb.MockImageHandle{Reference_: ref, LastUsedAt_: oldArtifactTime()}
}

// staleSummary returns the aggregate prune summary line for the given counts.
func staleSummary(dryRun string, vms, vols, msbImgs int) string {
	return fmt.Sprintf("%s %d VMs, %d home volumes, 0 docker images, %d msb images",
		dryRun, vms, vols, msbImgs)
}

func TestPrune(t *testing.T) {
	t.Run("no stale items", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, nil, staleSummary("Pruned", 0, 0, 0))
			})
		}
	})

	t.Run("dry run with stale items", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, append([]string{"--dry-run"}, flags...), func() {
					m := &sandboxmsb.MockMsbClient{}
					m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
					m.Sandboxes = append(m.Sandboxes,
						mkActiveVM("activeproject-1mjusbm3wikhb0"))
					m.Volumes = append(m.Volumes,
						homeVol("agents-sandbox-home-projectname-1mjusbm3wikhb0-20260806T143022"))
					m.Images = append(m.Images,
						msbImg("agents-sandbox/runner-projectname-1mjusbm3wikhb0:xyz789"))
					sandboxmsb.WithMsbMock(t, m)
				}, staleSummary("dry-run: Would prune", 1, 1, 1))
			})
		}
	})

	t.Run("partial failure", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, func() {
					m := &sandboxmsb.MockMsbClient{}
					m.Sandboxes = append(m.Sandboxes, &sandboxmsb.MockSandboxHandle{
						Name_:      "agents-sandbox-vm-first-1mjusbm3wikhb0",
						Status_:    msb.SandboxStatusStopped,
						UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
					})
					m.Sandboxes = append(m.Sandboxes, &sandboxmsb.MockSandboxHandle{
						Name_:      "agents-sandbox-vm-second-1mjusbm3wikhb0",
						Status_:    msb.SandboxStatusStopped,
						UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
					})
					m.Volumes = append(m.Volumes,
						homeVol("agents-sandbox-home-first-1mjusbm3wikhb0-20260806T143022"))
					m.Images = append(m.Images,
						msbImg("agents-sandbox/runner-second-1mjusbm3wikhb0:v1"))
					sandboxmsb.WithMsbMock(t, m)
				}, staleSummary("Pruned", 2, 1, 1))
			})
		}
	})

	t.Run("custom age of two weeks", func(t *testing.T) {
		runPruneTestWithAge(t, "2w", func(m *sandboxmsb.MockMsbClient) {
			m.Sandboxes = append(m.Sandboxes, &sandboxmsb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-staleproject-1mjusbm3wikhb0",
				Status_:    msb.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
			})
		}, staleSummary("Pruned", 1, 0, 0))
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
					msbImg("agents-sandbox/runner-stale-1mjusbm3wikhb0:v2"))

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

	t.Run("stale VMs and task sandboxes pruned", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, func() {
					m := &sandboxmsb.MockMsbClient{}
					m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
					m.Sandboxes = append(m.Sandboxes, mkStaleTask(time.Now().Add(-15*24*time.Hour)))
					m.Volumes = append(m.Volumes,
						homeVol("agents-sandbox-home-projectname-1mjusbm3wikhb0-20260806T143022"))
					m.Images = append(m.Images,
						msbImg("agents-sandbox/runner-projectname-1mjusbm3wikhb0:v2"))
					sandboxmsb.WithMsbMock(t, m)
				}, staleSummary("Pruned", 2, 1, 1))
			})
		}
	})

	t.Run("task sandboxes pruned", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, func() {
					if err := state.WriteState(
						state.Key{Slug: "activeproject-1mjusbm3wikhb0", Agent: "opencode"},
						state.HomeState{
							HomeVolume: "agents-sandbox-home-activeproject-1mjusbm3wikhb0-abc123",
						},
					); err != nil {
						t.Fatalf("WriteState: %v", err)
					}

					m := &sandboxmsb.MockMsbClient{}
					m.Sandboxes = append(m.Sandboxes,
						mkActiveVM("activeproject-1mjusbm3wikhb0-opencode"))
					m.Sandboxes = append(m.Sandboxes, mkStaleTask(time.Now().Add(-15*24*time.Hour)))
					m.Volumes = append(m.Volumes,
						homeVol("agents-sandbox-home-activeproject-1mjusbm3wikhb0-abc123"))
					m.Images = append(m.Images,
						msbImg("agents-sandbox/runner-activeproject-1mjusbm3wikhb0:opencode-latest"))
					sandboxmsb.WithMsbMock(t, m)
				}, staleSummary("Pruned", 1, 0, 0))
			})
		}
	})

	t.Run("flag fixture variations", func(t *testing.T) {
		for _, flags := range pruneAgeFlags {
			t.Run(strings.Join(flags, " "), func(t *testing.T) {
				runPruneTest(t, flags, func() {
					m := &sandboxmsb.MockMsbClient{}
					// Use 15d staleness to work with all flag values (7d, 7d, 14d, 14d).
					m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
					m.Sandboxes = append(m.Sandboxes,
						mkActiveVM("prod-main-1mjusbm3wikhb0"))
					m.Volumes = append(m.Volumes,
						homeVol("agents-sandbox-home-projectname-1mjusbm3wikhb0-20260806T143022"))
					m.Images = append(m.Images,
						msbImg("agents-sandbox/runner-projectname-1mjusbm3wikhb0:v2"))
					sandboxmsb.WithMsbMock(t, m)
				}, staleSummary("Pruned", 1, 1, 1))
			})
		}
	})
}

func runPruneTest(t *testing.T, flags []string, setupMocks func(), expected string) {
	t.Helper()
	cmd, ui := setupCommandFixtures(t, append([]string{"prune"}, flags...)...)
	if setupMocks != nil {
		setupMocks()
	}

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

// TestPruneCatalogError covers the live-state build failing during prune, which
// surfaces the raw ListSandboxes error from the command.
func TestPruneCatalogError(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "prune", "--age", "7d")
	mock := &sandboxmsb.MockMsbClient{}
	mock.ListSandboxesErr = errBoom
	sandboxmsb.WithMsbMock(t, mock)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when the prune catalog build fails")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected 'boom' error, got: %v", err)
	}
}

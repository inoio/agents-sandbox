package main

import (
	"encoding/json"
	"errors"
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
	"github.com/inoio/agents-sandbox/internal/termio"
)

type pruneJSONDetail struct {
	Type            string `json:"type"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Digest          string `json:"digest"`
	StaleForSeconds int64  `json:"stale_for_seconds"`
}

type pruneJSONReport struct {
	DryRun       bool              `json:"dry_run"`
	Sandboxes    int               `json:"sandboxes"`
	Volumes      int               `json:"volumes"`
	Images       int               `json:"images"`
	DockerImages int               `json:"docker_images"`
	Details      []pruneJSONDetail `json:"details"`
}

func unmarshalPruneJSON(t *testing.T, outCalls []string) pruneJSONReport {
	t.Helper()
	joined := strings.Join(outCalls, "\n")
	var got pruneJSONReport
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	return got
}

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

				cmd, ui := setupCommandFixtures(t, append([]string{"prune", "--yes"}, flags...)...)
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
					if err := seedHomeState(
						state.Key{Slug: "activeproject-1mjusbm3wikhb0", Agent: "opencode"},
						state.HomeState{
							HomeVolume: "agents-sandbox-home-activeproject-1mjusbm3wikhb0-abc123",
						},
					); err != nil {
						t.Fatalf("seedHomeState: %v", err)
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

func TestPruneNonInteractiveRequiresYes(t *testing.T) {
	vm := mkStaleVM(time.Now().Add(-15 * 24 * time.Hour))
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, vm)

	cmd, _ := setupCommandFixtures(t, "prune")
	sandboxmsb.WithMsbMock(t, m)

	err := cmd.Execute()
	if !errors.Is(err, termio.ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired, got: %v", err)
	}
	if vm.(*sandboxmsb.MockSandboxHandle).DidRemove() {
		t.Error("expected no removals")
	}
}

func TestPruneYesSkipsPrompt(t *testing.T) {
	vm := mkStaleVM(time.Now().Add(-15 * 24 * time.Hour))
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, vm)

	cmd, ui := setupCommandFixtures(t, "prune", "--yes")
	sandboxmsb.WithMsbMock(t, m)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.ConfirmCalls) != 0 {
		t.Errorf("expected no prompt, got: %v", ui.ConfirmCalls)
	}
	if !vm.(*sandboxmsb.MockSandboxHandle).DidRemove() {
		t.Error("expected the stale sandbox to be pruned")
	}
}

func TestPruneDryRunSkipsConfirmation(t *testing.T) {
	vm := mkStaleVM(time.Now().Add(-15 * 24 * time.Hour))
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, vm)

	cmd, ui := setupCommandFixtures(t, "prune", "--dry-run")
	sandboxmsb.WithMsbMock(t, m)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.ConfirmCalls) != 0 {
		t.Errorf("dry-run must not prompt, got: %v", ui.ConfirmCalls)
	}
	if vm.(*sandboxmsb.MockSandboxHandle).DidRemove() {
		t.Error("dry-run must not remove anything")
	}
	if !slices.Contains(ui.OutCalls, staleSummary("dry-run: Would prune", 1, 0, 0)) {
		t.Errorf("expected dry-run preview, got: %v", ui.OutCalls)
	}
}

func TestPruneDeclined(t *testing.T) {
	vm := mkStaleVM(time.Now().Add(-15 * 24 * time.Hour))
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, vm)

	cmd, ui := setupCommandFixtures(t, "prune")
	sandboxmsb.WithMsbMock(t, m)
	ui.IsInteractiveResult = true
	ui.ConfirmResult = false

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancelled error, got: %v", err)
	}
	if vm.(*sandboxmsb.MockSandboxHandle).DidRemove() {
		t.Error("expected no removals")
	}
}

func TestPruneFormatJSON(t *testing.T) {
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
	m.Volumes = append(m.Volumes,
		homeVol("agents-sandbox-home-projectname-1mjusbm3wikhb0-20260806T143022"))
	m.Images = append(m.Images,
		msbImg("agents-sandbox/runner-projectname-1mjusbm3wikhb0:v2"))

	cmd, ui := setupCommandFixtures(t, "prune", "--format", "json", "--yes")
	sandboxmsb.WithMsbMock(t, m)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := unmarshalPruneJSON(t, ui.OutCalls)
	if got.Sandboxes != 1 || got.Volumes != 1 || got.Images != 1 || got.DockerImages != 0 {
		t.Errorf("unexpected counts: %+v", got)
	}
	if got.DryRun {
		t.Error("dry_run = true, want false")
	}
	if len(got.Details) != 3 {
		t.Fatalf("expected 3 details, got %d: %+v", len(got.Details), got.Details)
	}
	for _, detail := range got.Details {
		if detail.Type == "" || detail.Name == "" || detail.Slug == "" {
			t.Errorf("detail missing fields: %+v", detail)
		}
		if detail.Type == "volume" {
			want := int64((30 * 24 * time.Hour).Seconds())
			if detail.StaleForSeconds < want-int64(time.Hour.Seconds()) ||
				detail.StaleForSeconds > want+int64(time.Hour.Seconds()) {
				t.Errorf("volume stale_for_seconds = %d, want ~%d", detail.StaleForSeconds, want)
			}
		}
	}
}

func TestPruneVerboseShowsDetails(t *testing.T) {
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))

	cmd, ui := setupCommandFixtures(t, "prune", "--yes")
	sandboxmsb.WithMsbMock(t, m)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.ContainsFunc(ui.VerboseCalls, func(s string) bool {
		return strings.Contains(s, "agents-sandbox-vm-projectname-1mjusbm3wikhb0")
	}) {
		t.Errorf("expected per-entry verbose detail, got: %v", ui.VerboseCalls)
	}
}

func TestPruneFormatUnknown(t *testing.T) {
	runPruneTestError(t, []string{"prune", "--format", "yaml"}, "unsupported format")
}

func TestPruneFormatJSONQuietConflict(t *testing.T) {
	runPruneTestError(t, []string{"prune", "--format", "json", "--quiet"}, "mutually exclusive")
}

func TestPruneQuietSuppressesOutput(t *testing.T) {
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))

	cmd, ui := setupCommandFixtures(t, "prune", "--quiet", "--yes")
	sandboxmsb.WithMsbMock(t, m)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("quiet prune should write no stdout, got: %v", ui.OutCalls)
	}
}

// TestPruneEnvQuietSuppressesOutput proves an env/config-driven quiet (no
// --quiet flag) still suppresses prune stdout, since the handler keys off the
// effective UI quiet rather than the raw flag.
func TestPruneEnvQuietSuppressesOutput(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_QUIET", "true")
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))

	cmd, ui := setupCommandFixtures(t, "prune", "--yes")
	sandboxmsb.WithMsbMock(t, m)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("env quiet prune should write no stdout, got: %v", ui.OutCalls)
	}
}

// TestPrunePartialErrorRendersReport proves a prune whose per-group errors are
// joined still renders the summary of what was removed before the error is
// returned.
func TestPrunePartialErrorRendersReport(t *testing.T) {
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
	m.ListImagesErr = errBoom

	cmd, ui := setupCommandFixtures(t, "prune", "--yes")
	sandboxmsb.WithMsbMock(t, m)

	err := cmd.Execute()
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected joined prune error, got: %v", err)
	}
	if !slices.Contains(ui.OutCalls, staleSummary("Pruned", 1, 0, 0)) {
		t.Errorf("expected partial summary despite error, got: %v", ui.OutCalls)
	}
}

func TestPrunePartialErrorRendersJSON(t *testing.T) {
	m := &sandboxmsb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, mkStaleVM(time.Now().Add(-15*24*time.Hour)))
	m.ListImagesErr = errBoom

	cmd, ui := setupCommandFixtures(t, "prune", "--format", "json", "--yes")
	sandboxmsb.WithMsbMock(t, m)

	err := cmd.Execute()
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected joined prune error, got: %v", err)
	}
	got := unmarshalPruneJSON(t, ui.OutCalls)
	if got.Sandboxes != 1 {
		t.Errorf("expected the removed sandbox in the report, got: %+v", got)
	}
}

func TestPruneOlderThan(t *testing.T) {
	runPruneTestWithFlags(t, []string{"--older-than", "2w"}, func(m *sandboxmsb.MockMsbClient) {
		m.Sandboxes = append(m.Sandboxes, &sandboxmsb.MockSandboxHandle{
			Name_:      "agents-sandbox-vm-staleproject-1mjusbm3wikhb0",
			Status_:    msb.SandboxStatusStopped,
			UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
		})
	}, staleSummary("Pruned", 1, 0, 0))
}

func TestPruneOlderThanInvalid(t *testing.T) {
	runPruneTestError(t, []string{"prune", "--older-than", "nope"}, "invalid age")
}

func TestPruneOlderThanAndAgeConflict(t *testing.T) {
	runPruneTestError(t, []string{"prune", "--older-than", "7d", "--age", "7d"},
		"use either --older-than or --age, not both")
}

func TestPruneAgeDeprecated(t *testing.T) {
	runPruneTestWithFlags(t, []string{"--age", "2w"}, func(m *sandboxmsb.MockMsbClient) {
		m.Sandboxes = append(m.Sandboxes, &sandboxmsb.MockSandboxHandle{
			Name_:      "agents-sandbox-vm-staleproject-1mjusbm3wikhb0",
			Status_:    msb.SandboxStatusStopped,
			UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
		})
	}, staleSummary("Pruned", 1, 0, 0))

	cmd, _ := setupCommandFixtures(t, "prune")
	pruneCmd, _, err := cmd.Find([]string{cmdPrune})
	if err != nil {
		t.Fatalf("find prune command: %v", err)
	}
	flag := pruneCmd.Flags().Lookup(flagAge)
	if flag == nil || flag.Deprecated == "" {
		t.Fatal("--age flag must be marked deprecated")
	}
}

func runPruneTestWithFlags(t *testing.T, flags []string, setupMock func(m *sandboxmsb.MockMsbClient), expected string) {
	t.Helper()
	mock := &sandboxmsb.MockMsbClient{}
	setupMock(mock)
	cmd, ui := setupCommandFixtures(t, append([]string{"prune", "--yes"}, flags...)...)
	sandboxmsb.WithMsbMock(t, mock)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkSummary(t, ui.OutCalls, expected)
}

func runPruneTest(t *testing.T, flags []string, setupMocks func(), expected string) {
	t.Helper()
	cmd, ui := setupCommandFixtures(t, append([]string{"prune", "--yes"}, flags...)...)
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
	cmd, ui := setupCommandFixtures(t, "prune", "--yes", "--age", age)
	sandboxmsb.WithMsbMock(t, mock)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkSummary(t, ui.OutCalls, expected)
}

func runPruneTestError(t *testing.T, args []string, wantErrContains string) {
	t.Helper()
	cmd, _ := setupCommandFixtures(t, append(args, "--yes")...)
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
	cmd, _ := setupCommandFixtures(t, "prune", "--age", "7d", "--yes")
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

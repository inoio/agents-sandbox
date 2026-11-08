package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// breakStateRoot turns the mock state root into a regular file, so reads of it
// fail with ENOTDIR rather than being mistaken for a missing root.
func breakStateRoot(t *testing.T) {
	t.Helper()
	root := configpaths.Get().UserStateDir()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPlanRemovalPropagatesErrors drives each planning failure through PlanRemoval
// so the top-level error returns are exercised, not only the helpers.
func TestPlanRemovalPropagatesErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("state keys", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		const slug = "purge-plan-keys-1mjusbm3wikhb0"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, dir, "")
		msb.WithMsbMock(t, &msb.MockMsbClient{})

		if _, err := PlanRemoval(ctx, []string{slug}, "", &ui); err == nil {
			t.Fatal("expected the state-key planning error")
		}
	})

	t.Run("root state", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		root := configpaths.Get().UserStateDir()
		if err := os.WriteFile(filepath.Join(root, "state.yaml"), []byte("schema: [not-valid"), 0o600); err != nil {
			t.Fatal(err)
		}
		msb.WithMsbMock(t, &msb.MockMsbClient{})

		if _, err := PlanRemoval(ctx, nil, "", &ui); err == nil {
			t.Fatal("expected the root-state read error")
		}
	})

	t.Run("volumes", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		msb.WithMsbMock(t, &msb.MockMsbClient{ListVolumesErr: errBoom})

		if _, err := PlanRemoval(ctx, nil, "", &ui); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("images", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		msb.WithMsbMock(t, &msb.MockMsbClient{ListImagesErr: errBoom})

		if _, err := PlanRemoval(ctx, nil, "", &ui); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("sandboxes", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		msb.WithMsbMock(t, &msb.MockMsbClient{ListSandboxesErr: errBoom})

		if _, err := PlanRemoval(ctx, nil, "", &ui); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("state files", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		const slug = "purge-plan-files-1mjusbm3wikhb0"
		volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		// The agent path is a regular file, so the key (added from the volume
		// name) has no statable state file.
		testutil.WritePath(t, filepath.Join(dir, "opencode"), "")
		msb.WithMsbMock(t, &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		})

		if _, err := PlanRemoval(ctx, []string{slug}, "", &ui); err == nil {
			t.Fatal("expected the state-file planning error")
		}
	})

	t.Run("claims", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		const slug = "purge-plan-claims-1mjusbm3wikhb0"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, filepath.Join(dir, "claims"), "")
		msb.WithMsbMock(t, &msb.MockMsbClient{})

		if _, err := PlanRemoval(ctx, []string{slug}, "", &ui); err == nil {
			t.Fatal("expected the claim planning error")
		}
	})
}

// TestPlanStateKeysPropagatesErrors pins the agent-list failure and the legacy
// state-file stat failure.
func TestPlanStateKeysPropagatesErrors(t *testing.T) {
	t.Run("agent list error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		const slug = "plan-keys-agent-err"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, dir, "")
		err := planStateKeys(map[string]struct{}{slug: {}}, func(state.Key) {})
		if err == nil {
			t.Fatal("expected the agent list error")
		}
	})

	t.Run("legacy state stat error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		const slug = "plan-keys-legacy-err"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("state.yaml", filepath.Join(dir, "state.yaml")); err != nil {
			t.Fatal(err)
		}
		err := planStateKeys(map[string]struct{}{slug: {}}, func(state.Key) {})
		if err == nil {
			t.Fatal("expected the legacy state stat error")
		}
	})
}

// TestPlanVolumesPropagatesErrors pins the volume-list failure and the
// state-reference scan failure.
func TestPlanVolumesPropagatesErrors(t *testing.T) {
	ctx := context.Background()
	scope := purgeScope{slugs: map[string]struct{}{}}

	t.Run("list error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		client := &msb.MockMsbClient{ListVolumesErr: errBoom}
		if _, err := planVolumes(
			ctx,
			client,
			scope,
			&state.RootState{},
			func(state.Key) {},
			&ui,
		); !errors.Is(
			err,
			errBoom,
		) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("reference scan error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := termio.NewTestMock(t)
		key := state.Key{Slug: "plan-vols-corrupt", Agent: "opencode"}
		if err := os.MkdirAll(state.KeyDir(key), 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, filepath.Join(state.KeyDir(key), "state.yaml"), "schema: [not-valid")
		client := &msb.MockMsbClient{}
		if _, err := planVolumes(ctx, client, scope, &state.RootState{}, func(state.Key) {}, &ui); err == nil {
			t.Fatal("expected the state-reference scan error")
		}
	})
}

// TestClassifyVolumeOwnersSkipsEmptyClaimVolume pins that a claim recording no
// volume owns nothing.
func TestClassifyVolumeOwnersSkipsEmptyClaimVolume(t *testing.T) {
	root := &state.RootState{
		AdoptedHomeVolumes: map[string]state.AdoptedHomeVolume{
			"legacy/opencode": {CheckoutSlug: "checkout", Volume: ""},
		},
	}
	scope := purgeScope{slugs: map[string]struct{}{"checkout": {}}}
	owned, elsewhere := classifyVolumeOwners(scope, map[string][]state.Key{}, root)
	if len(owned) != 0 || len(elsewhere) != 0 {
		t.Fatalf("owned = %v, elsewhere = %v, want both empty", owned, elsewhere)
	}
}

// TestPlanImagesPropagatesErrors pins the image-list failure and that a runner
// image of an unplanned slug is skipped.
func TestPlanImagesPropagatesErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("list error", func(t *testing.T) {
		client := &msb.MockMsbClient{ListImagesErr: errBoom}
		if _, err := planImages(ctx, client, nil, "", func(state.Key) {}); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("foreign slug skipped", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				&msb.MockImageHandle{Reference_: naming.RunnerImageRef("other-1mjusbm3wikhb0", "opencode")},
			},
		}
		var keys []state.Key
		images, err := planImages(
			ctx,
			client,
			map[string]struct{}{"wanted-1mjusbm3wikhb0": {}},
			"",
			func(k state.Key) { keys = append(keys, k) },
		)
		if err != nil {
			t.Fatalf("planImages: %v", err)
		}
		if len(images) != 0 || len(keys) != 0 {
			t.Errorf("images = %v, keys = %v, want none for an unplanned slug", images, keys)
		}
	})
}

// TestPlanSandboxesPropagatesErrors pins the sandbox-list failure and that a
// launcher sandbox of an unplanned slug is skipped.
func TestPlanSandboxesPropagatesErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("list error", func(t *testing.T) {
		client := &msb.MockMsbClient{ListSandboxesErr: errBoom}
		if _, _, err := planSandboxes(ctx, client, nil, "", func(state.Key) {}); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("foreign slug skipped", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:   naming.LegacyVMPrefix + "otherproject",
					Status_: msb.SandboxStatusStopped,
				},
			},
		}
		var keys []state.Key
		sandboxes, running, err := planSandboxes(
			ctx,
			client,
			map[string]struct{}{"wanted-1mjusbm3wikhb0": {}},
			"",
			func(k state.Key) { keys = append(keys, k) },
		)
		if err != nil {
			t.Fatalf("planSandboxes: %v", err)
		}
		if len(sandboxes) != 0 || running || len(keys) != 0 {
			t.Errorf("sandboxes = %v, running = %v, keys = %v, want none", sandboxes, running, keys)
		}
	})
}

// TestPlanAdoptionClaimsSkipsKeyWithoutSeparator pins that a malformed claim key
// is skipped rather than fabricating a key.
func TestPlanAdoptionClaimsSkipsKeyWithoutSeparator(t *testing.T) {
	root := &state.RootState{
		AdoptedHomeVolumes: map[string]state.AdoptedHomeVolume{
			"no-separator": {CheckoutSlug: "checkout", Volume: "volume"},
		},
	}
	scope := purgeScope{slugs: map[string]struct{}{"checkout": {}}}
	claims := planAdoptionClaims(scope, root, func(state.Key) {})
	if len(claims) != 0 {
		t.Fatalf("claims = %v, want none for a key without a separator", claims)
	}
}

// TestPlanStateFilesPropagatesError pins that a key whose state path is not
// statable fails the plan.
func TestPlanStateFilesPropagatesError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "plan-files-err"
	if err := os.MkdirAll(state.SlugDir(slug), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(state.SlugDir(slug), "opencode"), "")
	keys := map[state.Key]struct{}{{Slug: slug, Agent: "opencode"}: {}}
	if _, err := planStateFiles(keys); err == nil {
		t.Fatal("expected the state-file stat error")
	}
}

// TestPlanClaimsPropagatesError pins the claim-token read failure.
func TestPlanClaimsPropagatesError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "plan-claims-err"
	dir := state.SlugDir(slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(dir, "claims"), "")
	if _, err := planClaims(map[string]struct{}{slug: {}}); err == nil {
		t.Fatal("expected the claim-token read error")
	}
}

// TestResolveSandboxesSkipsMissingAndPropagatesOtherErrors pins the not-found
// skip and the fail-closed resolution error.
func TestResolveSandboxesSkipsMissingAndPropagatesOtherErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("missing sandbox skipped", func(t *testing.T) {
		p := &RemovalPlan{Sandboxes: []string{"missing-vm"}}
		client := &msb.MockMsbClient{
			GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
				return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
			},
		}
		handles, err := p.resolveSandboxes(ctx, client)
		if err != nil {
			t.Fatalf("resolveSandboxes: %v", err)
		}
		if len(handles) != 0 {
			t.Errorf("handles = %v, want none for a vanished sandbox", handles)
		}
	})

	t.Run("other error propagates", func(t *testing.T) {
		p := &RemovalPlan{Sandboxes: []string{"broken-vm"}}
		client := &msb.MockMsbClient{
			GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
				return nil, errBoom
			},
		}
		if _, err := p.resolveSandboxes(ctx, client); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})
}

// TestExecuteNilAndCancelled pins the nil-plan and cancelled-context guards.
func TestExecuteNilAndCancelled(t *testing.T) {
	ui := termio.NewTestMock(t)
	var nilPlan *RemovalPlan
	if err := nilPlan.Execute(context.Background(), false, false, &ui); err != nil {
		t.Fatalf("nil plan Execute = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&RemovalPlan{}).Execute(ctx, false, false, &ui); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Execute = %v, want context.Canceled", err)
	}
}

// TestExecutePropagatesSandboxResolutionError pins that a resolution failure
// aborts the purge before any deletion.
func TestExecutePropagatesSandboxResolutionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
			return nil, errBoom
		},
	})

	plan := &RemovalPlan{Sandboxes: []string{"broken-vm"}}
	if err := plan.Execute(context.Background(), false, false, &ui); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want %v", err, errBoom)
	}
}

// TestSweepStaleStatePropagatesErrors pins the claim-token read failure and the
// unheld-claim removal failure in the purge's step 1.
func TestSweepStaleStatePropagatesErrors(t *testing.T) {
	t.Run("token read error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		const slug = "purge-sweep-read"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, filepath.Join(dir, "claims"), "")
		plan := &RemovalPlan{Slugs: []string{slug}}
		if err := plan.sweepStaleState(); err == nil {
			t.Fatal("expected the claim-token read error")
		}
	})

	t.Run("claim removal error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		const slug = "purge-sweep-rm"
		claimsDir := filepath.Join(state.SlugDir(slug), "claims")
		if err := os.MkdirAll(claimsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, filepath.Join(claimsDir, "free.claim"), "")
		if err := os.Chmod(claimsDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(claimsDir, 0o700) })
		plan := &RemovalPlan{Slugs: []string{slug}}
		if err := plan.sweepStaleState(); err == nil {
			t.Fatal("expected the claim removal error")
		}
	})
}

// TestRemoveSandboxesWarnsAndJoinsFailures pins the stop failure and the removal
// failure of the sandbox step.
func TestRemoveSandboxesWarnsAndJoinsFailures(t *testing.T) {
	ui := termio.NewTestMock(t)
	running := &msb.MockSandboxHandle{Name_: "run-vm", Status_: msb.SandboxStatusRunning, StopErr: errBoom}
	stopped := &msb.MockSandboxHandle{Name_: "stopped-vm", Status_: msb.SandboxStatusStopped, RemoveErr: errBoom}

	errs := removeSandboxes(context.Background(), []msb.SandboxHandle{running, stopped}, &ui)
	if len(errs) != 2 {
		t.Errorf("errs = %v, want both the stop and remove failures", errs)
	}
	if len(ui.WarnCalls) != 2 {
		t.Errorf("WarnCalls = %v, want one warning per failure", ui.WarnCalls)
	}
	if !running.DidStop {
		t.Error("the running sandbox should have been asked to stop")
	}
}

// TestPruneSurvivingClaims pins the claim-retention rule: a planned claim is
// dropped when its volume was removed or no longer exists, but kept when its
// volume survived a failed removal — the surviving home's only owner record when
// the checkout's state is gone. A claim key not in the plan is never touched.
func TestPruneSurvivingClaims(t *testing.T) {
	const volume = "legacy-volume"
	const slug = "purge-claimkeep-1mjusbm3wikhb0"
	cases := []struct {
		name           string
		volumes        []string
		removed        map[string]struct{}
		claims         []string
		wantClaimDrops bool
	}{
		{
			name:           "removed volume drops the claim",
			volumes:        []string{volume},
			removed:        map[string]struct{}{volume: {}},
			claims:         []string{slug + "/opencode"},
			wantClaimDrops: true,
		},
		{
			name:           "absent volume drops the claim",
			claims:         []string{slug + "/opencode"},
			wantClaimDrops: true,
		},
		{
			name:    "surviving volume keeps the claim",
			volumes: []string{volume},
			claims:  []string{slug + "/opencode"},
		},
		{
			name:   "unmatched claim key is a no-op",
			claims: []string{slug + "/absent"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			seedAdoptionClaim(t, slug, "opencode", slug, volume)
			root, err := state.ReadRootState()
			if err != nil {
				t.Fatalf("ReadRootState: %v", err)
			}
			plan := &RemovalPlan{Volumes: tc.volumes, AdoptionClaims: tc.claims}
			changed := plan.pruneSurvivingClaims(root, tc.removed)
			_, kept := root.AdoptedHomeVolume(slug, "opencode")
			if kept == tc.wantClaimDrops {
				t.Errorf("claim kept = %v, want kept = %v", kept, !tc.wantClaimDrops)
			}
			if changed != tc.wantClaimDrops {
				t.Errorf("changed = %v, want %v", changed, tc.wantClaimDrops)
			}
		})
	}
}

// TestExecuteJoinsOwnershipUpdateFailure pins that a root-state write failure
// while pruning claims warns, is joined, and does not abort the rest.
func TestExecuteJoinsOwnershipUpdateFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	const slug = "purge-claimfail-1mjusbm3wikhb0"
	seedAdoptionClaim(t, slug, "opencode", slug, "legacy-volume")
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &ui)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if len(plan.AdoptionClaims) == 0 {
		t.Fatal("expected the checkout's adoption claim to be planned")
	}

	root := configpaths.Get().UserStateDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	if err := plan.Execute(context.Background(), false, false, &ui); err == nil {
		t.Fatal("expected the ownership-update failure to be joined")
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool { return strings.Contains(s, "home-volume ownership") }) {
		t.Errorf("WarnCalls = %v, want a warning naming the ownership-update failure", ui.WarnCalls)
	}
}

// TestExecuteJoinsStateAndDirRemovalFailures pins the fail-closed state removal
// and the empty-dir reclaim failures: a state dir that cannot be written keeps
// the state file, a slug dir that cannot be written keeps its claims dir, and
// both errors are joined.
func TestExecuteJoinsStateAndDirRemovalFailures(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	const slug = "purge-rmfail-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(state.SlugDir(slug), "claims"), 0o700); err != nil {
		t.Fatal(err)
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
	})

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &ui)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}

	// Neither the agent dir (the state file's parent) nor the slug dir (the
	// claims dir's parent) is writable, so both removals fail.
	for _, dir := range []string{state.KeyDir(agentKey), state.SlugDir(slug)} {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}

	if err := plan.Execute(context.Background(), false, false, &ui); err == nil {
		t.Fatal("expected the state and dir removal failures to be joined")
	}
	if len(ui.WarnCalls) < 2 {
		t.Errorf("WarnCalls = %v, want a warning for the state and the claims-dir failures", ui.WarnCalls)
	}
	if _, statErr := os.Stat(filepath.Join(state.KeyDir(agentKey), "state.yaml")); statErr != nil {
		t.Errorf("the state file must survive a failed removal, stat err = %v", statErr)
	}
	if _, readErr := state.ReadState(agentKey); readErr != nil {
		t.Errorf("the state pointer must survive, read err = %v", readErr)
	}
}

// TestStateRemovableAfterVolumes pins the tear-down predicate: a state with no
// volume is removable, a settled or pending volume follows the removed set, and
// a surviving volume keeps the state.
func TestStateRemovableAfterVolumes(t *testing.T) {
	predicate := stateRemovableAfterVolumes(map[string]struct{}{"removed": {}})
	cases := []struct {
		name  string
		state state.HomeState
		want  bool
	}{
		{"no volume", state.HomeState{}, true},
		{"settled volume removed", state.HomeState{HomeVolume: "removed"}, true},
		{"settled volume surviving", state.HomeState{HomeVolume: "survivor"}, false},
		{"recovered volume removed", state.HomeState{RecoveredHomeVolume: "removed"}, true},
		{"recovered volume surviving", state.HomeState{RecoveredHomeVolume: "survivor"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := predicate(&tc.state); got != tc.want {
				t.Errorf("removable = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlanRemovalScopesAdoptionClaimsToAgent pins that an agent-scoped purge
// owns only that agent's adoption claim and the volume behind it: another
// agent's claim (and any volume it names) is foreign to the scope and survives.
func TestPlanRemovalScopesAdoptionClaimsToAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	const (
		checkout       = "scope-claims-checkout-2nkvtdn4xjlic1"
		legacyPi       = "scope-claims-pi-1mjusbm3wikhb0"
		legacyOpencode = "scope-claims-oc-1mjusbm3wikhb0"
	)
	piVolume := naming.HomeVolumeName(legacyPi, "pi", "20260101T000000")
	ocVolume := naming.HomeVolumeName(legacyOpencode, "opencode", "20260101T000000")
	seedAdoptionClaim(t, legacyPi, "pi", checkout, piVolume)
	seedAdoptionClaim(t, legacyOpencode, "opencode", checkout, ocVolume)
	mock := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: piVolume},
			&msb.MockVolumeHandle{Name_: ocVolume},
		},
	}
	msb.WithMsbMock(t, mock)

	plan, err := PlanRemoval(context.Background(), []string{checkout}, "pi", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if want := []string{legacyPi + "/pi"}; !slices.Equal(plan.AdoptionClaims, want) {
		t.Errorf("AdoptionClaims = %v, want %v", plan.AdoptionClaims, want)
	}
	if want := []string{piVolume}; !slices.Equal(plan.Volumes, want) {
		t.Errorf("Volumes = %v, want %v", plan.Volumes, want)
	}

	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !slices.Contains(mock.RemovedVolumes, piVolume) {
		t.Errorf("RemovedVolumes = %v, want to contain %s", mock.RemovedVolumes, piVolume)
	}
	if slices.Contains(mock.RemovedVolumes, ocVolume) {
		t.Errorf("another agent's volume %q must survive, got %v", ocVolume, mock.RemovedVolumes)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume(legacyOpencode, "opencode"); !ok {
		t.Error("another agent's adoption claim must survive the scoped purge")
	}
}

// TestExecuteRefusesVolumeAdoptedAfterPlanning pins the purge/adoption race: a
// volume another checkout adopts while the confirmation prompt is open must not
// be deleted, and its new owner record must survive, because ownership is
// re-derived under the root lock at execution time.
func TestExecuteRefusesVolumeAdoptedAfterPlanning(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	const (
		legacy   = "purge-lateadopt-1mjusbm3wikhb0"
		checkout = "purge-latecheckout-2nkvtdn4xjlic1"
	)
	volumeName := naming.HomeVolumeName(legacy, "opencode", "20260101T000000")
	if err := seedHomeState(
		state.Key{Slug: legacy, Agent: "opencode"},
		state.HomeState{HomeVolume: volumeName},
	); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}
	mock := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
	}
	msb.WithMsbMock(t, mock)

	plan, err := PlanRemoval(context.Background(), []string{legacy}, "", &ui)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if !slices.Contains(plan.Volumes, volumeName) {
		t.Fatalf("Volumes = %v, want the legacy volume planned", plan.Volumes)
	}

	// Between planning and execution another checkout adopts the volume, so the
	// volume is no longer unmounted: the purge must refuse it.
	seedAdoptionClaim(t, legacy, "opencode", checkout, volumeName)

	if err := plan.Execute(context.Background(), false, false, &ui); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if slices.Contains(mock.RemovedVolumes, volumeName) {
		t.Errorf("RemovedVolumes = %v, want the adopted volume kept", mock.RemovedVolumes)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool {
		return strings.Contains(s, "adopted by another project")
	}) {
		t.Errorf("WarnCalls = %v, want the adoption refusal", ui.WarnCalls)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume(legacy, "opencode"); !ok {
		t.Error("the adopted home's claim must survive the refused purge")
	}
}

// TestExecuteFailsClosedWhenReferencesGoCorrupt pins the fail-closed re-derivation:
// if a project state file becomes unreadable between planning and execution, the
// ownership recheck cannot prove a volume is unreferenced, so the purge warns and
// removes nothing.
func TestExecuteFailsClosedWhenReferencesGoCorrupt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	const (
		target  = "purge-refcorrupt-1mjusbm3wikhb0"
		foreign = "purge-refcorrupt-other-1mjusbm3wikhb0"
	)
	volumeName := naming.HomeVolumeName(target, "opencode", "20260101T000000")
	if err := seedHomeState(
		state.Key{Slug: target, Agent: "opencode"},
		state.HomeState{HomeVolume: volumeName},
	); err != nil {
		t.Fatalf("seed target state: %v", err)
	}
	mock := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
	}
	msb.WithMsbMock(t, mock)

	plan, err := PlanRemoval(context.Background(), []string{target}, "", &ui)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}

	// A state file outside the target's keys goes corrupt after planning.
	foreignKey := state.Key{Slug: foreign, Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(foreignKey), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(state.KeyDir(foreignKey), "state.yaml"), []byte("schema: [not-valid"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := plan.Execute(context.Background(), false, false, &ui); err == nil {
		t.Fatal("expected the reference-read failure to be joined")
	}
	if slices.Contains(mock.RemovedVolumes, volumeName) {
		t.Errorf("RemovedVolumes = %v, want nothing removed on a failed reference read", mock.RemovedVolumes)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool {
		return strings.Contains(s, "home-volume ownership")
	}) {
		t.Errorf("WarnCalls = %v, want the ownership-update failure warning", ui.WarnCalls)
	}
}

// TestPlanRemovalScopesVolumesByAgent pins agent scoping of volume ownership: a
// volume referenced solely by the scoped agent's state is planned, while one
// referenced solely by another agent's state is refused, so a single-key purge
// never deletes another agent's home.
func TestPlanRemovalScopesVolumesByAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "agentscope-1mjusbm3wikhb0"
	ownedVolume := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	foreignVolume := naming.HomeVolumeName(slug, "pi", "20260101T000000")
	if err := seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: ownedVolume},
	); err != nil {
		t.Fatalf("seed scoped state: %v", err)
	}
	if err := seedHomeState(
		state.Key{Slug: slug, Agent: "pi"},
		state.HomeState{HomeVolume: foreignVolume},
	); err != nil {
		t.Fatalf("seed foreign state: %v", err)
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: ownedVolume, CreatedAt_: time.Now()},
			&msb.MockVolumeHandle{Name_: foreignVolume, CreatedAt_: time.Now()},
		},
	})

	plan, err := PlanRemoval(context.Background(), []string{slug}, "opencode", &termio.Mock{})
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if want := []string{ownedVolume}; !slices.Equal(plan.Volumes, want) {
		t.Errorf("plan.Volumes = %v, want only the scoped agent's volume %v", plan.Volumes, want)
	}
}

// TestStateFileExistsMissingStateIsAbsent pins that a missing state file is
// absence, not "exists": planStateFiles must never list a path that does not
// exist on disk.
func TestStateFileExistsMissingStateIsAbsent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	exists, err := stateFileExists(state.Key{Slug: "missing-1mjusbm3wikhb0", Agent: "opencode"})
	if err != nil {
		t.Fatalf("stateFileExists: %v", err)
	}
	if exists {
		t.Error("stateFileExists = true for a missing state file, want false")
	}
}

// TestSortedStringsOrdersLexicographically pins that a plan's string sets render
// in stable sorted order rather than map-iteration order.
func TestSortedStringsOrdersLexicographically(t *testing.T) {
	got := sortedStrings(map[string]struct{}{"c": {}, "a": {}, "b": {}})
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("sortedStrings = %v, want %v", got, want)
	}
}

// TestPlanRemovalContinuesPastForeignAndBlockedArtifacts pins that a foreign or
// out-of-scope artifact ahead of an owned one does not end its list: the owned
// volume, image and sandbox listed last are still planned. A skip mutated from
// `continue` to `break` would silently under-plan the purge.
func TestPlanRemovalContinuesPastForeignAndBlockedArtifacts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		owned = "purgemix-1mjusbm3wikhb0"
		other = "purgemixother-2nkvtdn4xjlic1"
	)
	ownedVolume := naming.HomeVolumeName(owned, "opencode", "20260101T000000")
	blockedVolume := naming.HomeVolumeName(owned, "pi", "20260101T000000")
	ownedImage := naming.RunnerImageRef(owned, "opencode")
	unownedImage := naming.RunnerImageRef(other, "opencode")
	ownedSandbox := owned + "-opencode"
	unownedSandbox := other + "-opencode"

	if err := seedHomeState(
		state.Key{Slug: owned, Agent: "opencode"},
		state.HomeState{HomeVolume: ownedVolume},
	); err != nil {
		t.Fatalf("seed owned state: %v", err)
	}
	// blockedVolume is referenced in scope and by another checkout, so it is
	// refused rather than planned; a break there would drop the owned volume.
	for _, key := range []state.Key{
		{Slug: owned, Agent: "pi"},
		{Slug: other, Agent: "pi"},
	} {
		if err := seedHomeState(key, state.HomeState{HomeVolume: blockedVolume}); err != nil {
			t.Fatalf("seed blocked state: %v", err)
		}
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "user-data", CreatedAt_: time.Now()},
			&msb.MockVolumeHandle{Name_: blockedVolume, CreatedAt_: time.Now()},
			&msb.MockVolumeHandle{Name_: ownedVolume, CreatedAt_: time.Now()},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "docker.io/library/alpine:latest", LastUsedAt_: time.Now()},
			&msb.MockImageHandle{Reference_: unownedImage, LastUsedAt_: time.Now()},
			&msb.MockImageHandle{Reference_: ownedImage, LastUsedAt_: time.Now()},
		},
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{Name_: "builder-vm", Status_: msb.SandboxStatusRunning, UpdatedAt_: time.Now()},
			&msb.MockSandboxHandle{Name_: unownedSandbox, Status_: msb.SandboxStatusStopped, UpdatedAt_: time.Now()},
			&msb.MockSandboxHandle{Name_: ownedSandbox, Status_: msb.SandboxStatusStopped, UpdatedAt_: time.Now()},
		},
	})

	plan, err := PlanRemoval(context.Background(), []string{owned}, "", &termio.Mock{})
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if want := []string{ownedVolume}; !slices.Equal(plan.Volumes, want) {
		t.Errorf("plan.Volumes = %v, want %v", plan.Volumes, want)
	}
	if want := []string{ownedImage}; !slices.Equal(plan.Images, want) {
		t.Errorf("plan.Images = %v, want %v", plan.Images, want)
	}
	if want := []string{ownedSandbox}; !slices.Equal(plan.Sandboxes, want) {
		t.Errorf("plan.Sandboxes = %v, want %v", plan.Sandboxes, want)
	}
}

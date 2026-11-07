package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
					Status_: msbSdk.SandboxStatusStopped,
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
	running := &msb.MockSandboxHandle{Name_: "run-vm", Status_: msbSdk.SandboxStatusRunning, StopErr: errBoom}
	stopped := &msb.MockSandboxHandle{Name_: "stopped-vm", Status_: msbSdk.SandboxStatusStopped, RemoveErr: errBoom}

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

// TestRemoveAdoptionClaimsNoChange pins that removing claims that are not present
// is a silent no-op that leaves the root state untouched.
func TestRemoveAdoptionClaimsNoChange(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "purge-claimnoop-1mjusbm3wikhb0"
	seedAdoptionClaim(t, slug, "opencode", slug, "legacy-volume")

	plan := &RemovalPlan{AdoptionClaims: []string{slug + "/absent"}}
	if err := plan.removeAdoptionClaims(); err != nil {
		t.Fatalf("removeAdoptionClaims: %v", err)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume(slug, "opencode"); !ok {
		t.Error("an unmatched claim key must not drop the real claim")
	}
}

// TestExecuteJoinsAdoptionClaimRemovalFailure pins that a root-state write
// failure during claim removal warns, is joined, and does not abort the rest.
func TestExecuteJoinsAdoptionClaimRemovalFailure(t *testing.T) {
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
		t.Fatal("expected the adoption-claim removal failure to be joined")
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool { return strings.Contains(s, "adoption claims") }) {
		t.Errorf("WarnCalls = %v, want a warning naming the adoption-claim failure", ui.WarnCalls)
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

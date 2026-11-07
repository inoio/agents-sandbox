package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// TestEnumerateCandidatesFindsSlugWithNoStateDir pins the union's core promise:
// a project whose state directory was deleted by hand still surfaces through its
// leftover home volume, so it can be offered for retirement.
func TestEnumerateCandidatesFindsSlugWithNoStateDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "leftover-1mjusbm3wikhb0"
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_:      naming.HomeVolumeName(slug, "opencode", "20260101T000000"),
				CreatedAt_: time.Now().Add(-48 * time.Hour),
			},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", candidates)
	}
	got := candidates[0]
	if got.Slug != slug {
		t.Errorf("Slug = %q, want %q", got.Slug, slug)
	}
	wantKeys := []state.Key{{Slug: slug, Agent: "opencode"}}
	if !slices.Equal(got.Keys, wantKeys) {
		t.Errorf("Keys = %v, want %v", got.Keys, wantKeys)
	}
}

// TestEnumerateCandidatesNewestActivityFromVolumeAndImage pins that the activity
// is the maximum of every signal the union carries: the project VM's UpdatedAt,
// the newest volume's CreatedAt, and the newest image's LastUsedAt (falling back
// to its CreatedAt).
func TestEnumerateCandidatesNewestActivityFromVolumeAndImage(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "activity-1mjusbm3wikhb0"

	volumeTime := time.Now().Add(-72 * time.Hour)
	imageTime := time.Now().Add(-2 * time.Hour)
	vmTime := time.Now().Add(-100 * time.Hour)
	fallbackTime := time.Now().Add(-120 * time.Hour)

	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_:      naming.HomeVolumeName(slug, "opencode", "20260101T000000"),
				CreatedAt_: volumeTime,
			},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_:  naming.RunnerImageRef(slug, "opencode"),
				LastUsedAt_: imageTime,
			},
			// Never used: its CreatedAt is the fallback signal, older than the
			// used image, so it must not dominate.
			&msb.MockImageHandle{
				Reference_: naming.RunnerImageRef(slug, "codex"),
				CreatedAt_: fallbackTime,
			},
		},
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      slug + "-opencode",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: vmTime,
			},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", candidates)
	}
	if got := candidates[0].NewestActivity; !got.Equal(imageTime) {
		t.Errorf("NewestActivity = %v, want the newest image use %v", got, imageTime)
	}
}

// TestCandidateRetiredRequiresNoKeptSandboxAndAge pins the retirement predicate:
// a kept sandbox or a running task blocks retirement, and a project within (or
// exactly at) the age threshold is not retired.
func TestCandidateRetiredRequiresNoKeptSandboxAndAge(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	const age = time.Hour

	tests := []struct {
		name      string
		candidate Candidate
		want      bool
	}{
		{
			name:      "no kept sandbox, aged",
			candidate: Candidate{NewestActivity: now.Add(-2 * time.Hour)},
			want:      true,
		},
		{
			name:      "kept sandbox blocks retirement",
			candidate: Candidate{NewestActivity: now.Add(-2 * time.Hour), HasKeptSandbox: true},
			want:      false,
		},
		{
			name:      "running task blocks retirement",
			candidate: Candidate{NewestActivity: now.Add(-2 * time.Hour), HasRunningTask: true},
			want:      false,
		},
		{
			name:      "activity within the age is not retired",
			candidate: Candidate{NewestActivity: now.Add(-30 * time.Minute)},
			want:      false,
		},
		{
			name:      "exactly at the threshold is not retired",
			candidate: Candidate{NewestActivity: now.Add(-age)},
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.candidate.Retired(now, age); got != tt.want {
				t.Errorf("Retired = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCandidateNotRetiredWithRunningTask pins that a running task sandbox marks
// the project as having a running task and keeps it out of the retired set even
// when every artifact is otherwise aged; an inactive task must not.
func TestCandidateNotRetiredWithRunningTask(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "task-1mjusbm3wikhb0"

	msb.WithMsbMock(t, &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      naming.TaskSandboxName(slug, "opencode", 1729000000000000000),
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: time.Now().Add(-100 * time.Hour),
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_:      naming.HomeVolumeName(slug, "opencode", "20260101T000000"),
				CreatedAt_: time.Now().Add(-100 * time.Hour),
			},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", candidates)
	}
	got := candidates[0]
	if !got.HasRunningTask {
		t.Error("HasRunningTask = false, want true for a running task sandbox")
	}
	if got.HasKeptSandbox {
		t.Error("a task sandbox must not count as a kept sandbox")
	}
	if got.Retired(time.Now(), 7*24*time.Hour) {
		t.Error("a running task must block retirement")
	}
}

// TestEnumerateCandidatesIgnoresForeignArtifacts pins that artifacts that do not
// parse to a launcher slug are never candidates: a foreign volume, a foreign
// image, and a foreign sandbox whose name carries no launcher slug hash all
// contribute nothing.
func TestEnumerateCandidatesIgnoresForeignArtifacts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "user-data", CreatedAt_: time.Now()},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "docker.io/library/alpine:latest", LastUsedAt_: time.Now()},
			&msb.MockImageHandle{Reference_: "not-a-slug-hash:latest", LastUsedAt_: time.Now()},
		},
		Sandboxes: []msb.SandboxHandle{
			// A sandbox the user created directly with msb: no launcher labels,
			// no legacy prefix, no slug hash, so it is not ours.
			&msb.MockSandboxHandle{
				Name_:      "builder-vm",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: time.Now(),
			},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none for foreign artifacts", candidates)
	}
}

// TestEnumerateCandidatesAdmitsLauncherOwnedSandboxes pins the positive side of
// the ownership gate: a hash-anchored sandbox name and a legacy-prefixed name
// each surface their slug, so the gate rejects only genuinely foreign sandboxes.
func TestEnumerateCandidatesAdmitsLauncherOwnedSandboxes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	msb.WithMsbMock(t, &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "myproject-1mjusbm3wikhb0-opencode",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: time.Now(),
			},
			&msb.MockSandboxHandle{
				Name_:      naming.LegacyVMPrefix + "legacyproject",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: time.Now(),
			},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	gotSlugs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		gotSlugs = append(gotSlugs, candidate.Slug)
	}
	wantSlugs := []string{"legacyproject", "myproject-1mjusbm3wikhb0"}
	if !slices.Equal(gotSlugs, wantSlugs) {
		t.Fatalf("candidate slugs = %v, want %v", gotSlugs, wantSlugs)
	}
}

// TestEnumerateCandidatesSkipsMalformedLegacySandbox pins the slug guard: a
// launcher-prefixed name that strips to no parsed slug (a bare "agents-sandbox-vm-")
// is launcher-owned by its prefix but must not surface an empty-slug candidate a
// purge could target.
func TestEnumerateCandidatesSkipsMalformedLegacySandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	msb.WithMsbMock(t, &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{Name_: naming.LegacyVMPrefix, Status_: msbSdk.SandboxStatusStopped},
			&msb.MockSandboxHandle{
				Name_:      "myproject-1mjusbm3wikhb0-opencode",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: time.Now(),
			},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Slug != "myproject-1mjusbm3wikhb0" {
		t.Fatalf("candidates = %+v, want only the well-formed slug", candidates)
	}
}

// TestEnumerateCandidatesIncludesStateKeysAndModTime pins the state-directory
// source: a slug whose only trace is a per-agent state file surfaces with that
// key, and the state file's mtime is an activity signal.
func TestEnumerateCandidatesIncludesStateKeysAndModTime(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "stateonly-1mjusbm3wikhb0"
	key := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	mod := time.Now().Add(-time.Hour).Truncate(time.Second)
	statePath := filepath.Join(state.KeyDir(key), "state.yaml")
	if err := os.Chtimes(statePath, mod, mod); err != nil {
		t.Fatalf("chtimes state: %v", err)
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", candidates)
	}
	got := candidates[0]
	if !slices.Equal(got.Keys, []state.Key{key}) {
		t.Errorf("Keys = %v, want %v", got.Keys, []state.Key{key})
	}
	if !got.NewestActivity.Equal(mod) {
		t.Errorf("NewestActivity = %v, want the state mtime %v", got.NewestActivity, mod)
	}
}

// TestEnumerateCandidatesIncludesAdoptionClaims pins the root-state source: an
// adoption claim surfaces its owning checkout as a candidate — the identity whose
// purge can remove the claim — even when no state directory, volume, image or
// sandbox remains, and the claim's (ownerless) legacy key does not surface.
func TestEnumerateCandidatesIncludesAdoptionClaims(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		legacy   = "adopted-1mjusbm3wikhb0"
		checkout = "adopting-checkout-2nkvtdn4xjlic1"
	)
	seedAdoptionClaim(t, legacy, "opencode", checkout, "legacy-volume")
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", candidates)
	}
	if candidates[0].Slug != checkout {
		t.Errorf("Slug = %q, want the owning checkout %q", candidates[0].Slug, checkout)
	}
	if got := candidates[0].Keys; !slices.Equal(got, []state.Key{{Slug: checkout, Agent: "opencode"}}) {
		t.Errorf("Keys = %v, want the owning checkout's key", got)
	}
}

func seedAdoptionClaim(t *testing.T, slug, agent, checkoutSlug, volume string) {
	t.Helper()
	_, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		txn.State().ClaimAdoptedHomeVolume(slug, agent, checkoutSlug, volume)
		return txn.Commit()
	})
	if err != nil {
		t.Fatalf("seed adoption claim: %v", err)
	}
}

// emptyBuilderFor returns a builder lookup that folds nothing, so an enumerator
// can be driven in isolation.
func emptyBuilderFor(string) *candidateBuilder {
	return &candidateBuilder{keys: map[state.Key]struct{}{}}
}

// TestEnumerateCandidatesPropagatesSourceErrors pins that a failure in any one
// union source fails the whole enumeration rather than silently dropping that
// source's artifacts.
func TestEnumerateCandidatesPropagatesSourceErrors(t *testing.T) {
	t.Run("state enumeration", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		breakStateRoot(t)
		msb.WithMsbMock(t, &msb.MockMsbClient{})
		if _, err := EnumerateCandidates(context.Background(), time.Hour); err == nil {
			t.Fatal("expected the state enumeration error to propagate")
		}
	})

	t.Run("volume enumeration", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		msb.WithMsbMock(t, &msb.MockMsbClient{ListVolumesErr: errBoom})
		if _, err := EnumerateCandidates(context.Background(), time.Hour); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("volume references", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		key := state.Key{Slug: "cand-corrupt-1mjusbm3wikhb0", Agent: "opencode"}
		if err := os.MkdirAll(state.KeyDir(key), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(state.KeyDir(key), "state.yaml"), []byte("schema: [not-valid"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		msb.WithMsbMock(t, &msb.MockMsbClient{})
		if _, err := EnumerateCandidates(context.Background(), time.Hour); err == nil {
			t.Fatal("expected the volume-reference read error to propagate")
		}
	})

	t.Run("image enumeration", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		msb.WithMsbMock(t, &msb.MockMsbClient{ListImagesErr: errBoom})
		if _, err := EnumerateCandidates(context.Background(), time.Hour); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("sandbox enumeration", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		msb.WithMsbMock(t, &msb.MockMsbClient{ListSandboxesErr: errBoom})
		if _, err := EnumerateCandidates(context.Background(), time.Hour); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("adoption claims", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		root := configpaths.Get().UserStateDir()
		if err := os.WriteFile(filepath.Join(root, "state.yaml"), []byte("schema: [not-valid"), 0o600); err != nil {
			t.Fatalf("corrupt root state: %v", err)
		}
		msb.WithMsbMock(t, &msb.MockMsbClient{})
		if _, err := EnumerateCandidates(context.Background(), time.Hour); err == nil {
			t.Fatal("expected the adoption-claim read error to propagate")
		}
	})
}

// TestEnumerateStateCandidatesPropagatesErrors pins the state-source fail-closed
// paths: an unreadable state root, an unreadable slug dir, and a legacy state
// path that is not absent but cannot be stat'd.
func TestEnumerateStateCandidatesPropagatesErrors(t *testing.T) {
	t.Run("state root unreadable", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		breakStateRoot(t)
		if err := enumerateStateCandidates(emptyBuilderFor); err == nil {
			t.Fatal("expected the state-root read error")
		}
	})

	t.Run("slug dir unreadable", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		const slug = "cand-unreadable"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o300); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if err := enumerateStateCandidates(emptyBuilderFor); err == nil {
			t.Fatal("expected the StatefulAgentNames error")
		}
	})

	t.Run("legacy state not absent", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		const slug = "cand-legacy-loop"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		// A self-referential symlink stats as neither a file nor absent, so the
		// legacy noteStateFile path surfaces the stat error instead of treating
		// it as no state.
		if err := os.Symlink("state.yaml", filepath.Join(dir, "state.yaml")); err != nil {
			t.Fatal(err)
		}
		if err := enumerateStateCandidates(emptyBuilderFor); err == nil {
			t.Fatal("expected the legacy state stat error")
		}
	})

	t.Run("per-agent note error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		const slug = "cand-agent-note-err"
		key := state.Key{Slug: slug, Agent: "opencode"}
		if err := seedHomeState(key, state.HomeState{}); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		// StatefulAgentNames has already stat'd this state file, so the agent
		// note error is a stat failure a real filesystem only produces under a
		// race; the seam drives the fail-closed propagation.
		original := noteStateFile
		noteStateFile = func(*candidateBuilder, state.Key) error { return errBoom }
		t.Cleanup(func() { noteStateFile = original })

		if err := enumerateStateCandidates(emptyBuilderFor); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})
}

// TestEnumerateSourceListErrors pins the per-source list-failure returns, one
// for volumes, images and sandboxes.
func TestEnumerateSourceListErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ctx := context.Background()

	t.Run("volumes", func(t *testing.T) {
		msb.WithMsbMock(t, &msb.MockMsbClient{ListVolumesErr: errBoom})
		if err := enumerateVolumeCandidates(ctx, nil, nil, emptyBuilderFor); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("images", func(t *testing.T) {
		msb.WithMsbMock(t, &msb.MockMsbClient{ListImagesErr: errBoom})
		if err := enumerateImageCandidates(ctx, emptyBuilderFor); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("sandboxes", func(t *testing.T) {
		msb.WithMsbMock(t, &msb.MockMsbClient{ListSandboxesErr: errBoom})
		if err := enumerateSandboxCandidates(ctx, time.Hour, emptyBuilderFor); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})
}

// TestEnumerateAdoptionClaimsEdgeCases pins that a claim key carrying no
// separator is skipped rather than fabricating a slug.
func TestEnumerateAdoptionClaimsEdgeCases(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	var seen []string
	collect := func(slug string) *candidateBuilder {
		seen = append(seen, slug)
		return &candidateBuilder{keys: map[state.Key]struct{}{}}
	}
	enumerateAdoptionClaims(map[string]state.AdoptedHomeVolume{
		"no-separator": {CheckoutSlug: "checkout", Volume: "volume"},
	}, collect)
	if len(seen) != 0 {
		t.Errorf("builder slugs = %v, want none for a key without a separator", seen)
	}
}

// TestEnumerateCandidatesAttributesAdoptedVolumeToItsCheckout pins that a
// legacy-named volume a checkout adopted surfaces as that checkout, not as the
// ownerless legacy slug whose purge could not remove it. An orphan legacy-named
// volume, by contrast, still surfaces under its own name.
func TestEnumerateCandidatesAttributesAdoptedVolumeToItsCheckout(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		legacy   = "vollegacy-1mjusbm3wikhb0"
		checkout = "volcheckout-2nkvtdn4xjlic1"
		orphan   = "volorphan-1mjusbm3wikhb0"
	)
	adopted := naming.HomeVolumeName(legacy, "opencode", "20260101T000000")
	orphanVolume := naming.HomeVolumeName(orphan, "opencode", "20260101T000000")
	if err := seedHomeState(
		state.Key{Slug: checkout, Agent: "opencode"},
		state.HomeState{HomeVolume: adopted},
	); err != nil {
		t.Fatalf("seed checkout state: %v", err)
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: adopted, CreatedAt_: time.Now()},
			&msb.MockVolumeHandle{Name_: orphanVolume, CreatedAt_: time.Now()},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	bySlug := map[string][]state.Key{}
	for _, candidate := range candidates {
		bySlug[candidate.Slug] = candidate.Keys
	}
	if _, ok := bySlug[legacy]; ok {
		t.Errorf("the ownerless legacy slug %q must not surface; got %v", legacy, bySlug)
	}
	if want := []state.Key{{Slug: checkout, Agent: "opencode"}}; !slices.Equal(bySlug[checkout], want) {
		t.Errorf("checkout keys = %v, want %v", bySlug[checkout], want)
	}
	if want := []state.Key{{Slug: orphan, Agent: "opencode"}}; !slices.Equal(bySlug[orphan], want) {
		t.Errorf("orphan keys = %v, want %v", bySlug[orphan], want)
	}
}

// TestEnumerateCandidatesAdoptionClaimWithoutCheckout pins the fallback: a claim
// that records no owning checkout surfaces under its legacy key, so a malformed
// or pre-checkout claim is still discoverable.
func TestEnumerateCandidatesAdoptionClaimWithoutCheckout(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const legacy = "adopted-nocheckout-1mjusbm3wikhb0"
	seedAdoptionClaim(t, legacy, "opencode", "", "legacy-volume")
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Slug != legacy {
		t.Fatalf("candidates = %+v, want the legacy slug %q", candidates, legacy)
	}
	if want := []state.Key{{Slug: legacy, Agent: "opencode"}}; !slices.Equal(candidates[0].Keys, want) {
		t.Errorf("Keys = %v, want %v", candidates[0].Keys, want)
	}
}

// TestEnumerateCandidatesAttributesClaimOnlyVolumeToItsCheckout pins the
// lost-state adoption: when only an adoption claim records the checkout and its
// volume, the volume contributes its activity to the checkout, so the checkout
// is not listed as retired just because no state file references the volume.
func TestEnumerateCandidatesAttributesClaimOnlyVolumeToItsCheckout(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		legacy   = "claimonly-1mjusbm3wikhb0"
		checkout = "claimonlycheckout-2nkvtdn4xjlic1"
	)
	volumeName := naming.HomeVolumeName(legacy, "opencode", "20260101T000000")
	seedAdoptionClaim(t, legacy, "opencode", checkout, volumeName)
	createdAt := time.Now().Add(-2 * time.Hour)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName, CreatedAt_: createdAt}},
	})

	candidates, err := EnumerateCandidates(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly the checkout", candidates)
	}
	got := candidates[0]
	if got.Slug != checkout {
		t.Fatalf("Slug = %q, want the owning checkout %q", got.Slug, checkout)
	}
	if got.NewestActivity.Sub(createdAt).Abs() > time.Second {
		t.Errorf("NewestActivity = %v, want the volume's creation time %v", got.NewestActivity, createdAt)
	}
	if got.Retired(time.Now(), 24*time.Hour) {
		t.Error("a checkout with a 2h-old adopted volume must not be retired")
	}
}

// TestVolumeOwners pins the ownership index: a volume is owned by every state
// key that references it plus the checkout of every well-formed claim that names
// it, deduplicated, while a malformed or empty claim is ignored so it cannot
// fabricate an owner.
func TestVolumeOwners(t *testing.T) {
	references := map[string][]state.Key{
		"referenced": {{Slug: "state-slug", Agent: "opencode"}},
	}
	claims := map[string]state.AdoptedHomeVolume{
		// A claim on a referenced volume adds its checkout alongside the state key.
		"legacy/opencode": {CheckoutSlug: "checkout", Volume: "referenced"},
		// A second claim resolving to an existing owner is not duplicated.
		"legacy2/opencode": {CheckoutSlug: "state-slug", Volume: "referenced"},
		// A claim-only volume is owned by its checkout.
		"legacy/pi": {CheckoutSlug: "checkout", Volume: "claimed"},
		// These must be ignored: no separator, no checkout, no volume.
		"no-separator":        {CheckoutSlug: "checkout", Volume: "ignored"},
		"nocheckout/opencode": {Volume: "ignored"},
		"novolume/opencode":   {CheckoutSlug: "checkout"},
	}
	owners := volumeOwners(references, claims)

	want := map[string][]state.Key{
		"referenced": {
			{Slug: "state-slug", Agent: "opencode"},
			{Slug: "checkout", Agent: "opencode"},
		},
		"claimed": {{Slug: "checkout", Agent: "pi"}},
	}
	if len(owners) != len(want) {
		t.Fatalf("owners = %v, want %v", owners, want)
	}
	for name, wantKeys := range want {
		if !slices.Equal(owners[name], wantKeys) {
			t.Errorf("owners[%q] = %v, want %v", name, owners[name], wantKeys)
		}
	}
}

// TestEnumerateCandidatesContinuesPastNonMatchingArtifacts pins that a foreign
// volume, a foreign image and a task sandbox are skipped rather than ending
// their list: a matching artifact listed after a non-matching one still
// surfaces. A skip mutated from `continue` to `break` would drop it.
func TestEnumerateCandidatesContinuesPastNonMatchingArtifacts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		volSlug = "skipvol-1mjusbm3wikhb0"
		imgSlug = "skipimg-1mjusbm3wikhb0"
		sbSlug  = "skipsb-1mjusbm3wikhb0"
	)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "user-data", CreatedAt_: time.Now()},
			&msb.MockVolumeHandle{
				Name_:      naming.HomeVolumeName(volSlug, "opencode", "20260101T000000"),
				CreatedAt_: time.Now(),
			},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "docker.io/library/alpine:latest", LastUsedAt_: time.Now()},
			&msb.MockImageHandle{Reference_: naming.RunnerImageRef(imgSlug, "opencode"), LastUsedAt_: time.Now()},
		},
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      naming.TaskSandboxName("skiptask-1mjusbm3wikhb0", "opencode", 1729000000000000000),
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now(),
			},
			&msb.MockSandboxHandle{Name_: "builder-vm", Status_: msbSdk.SandboxStatusRunning, UpdatedAt_: time.Now()},
			&msb.MockSandboxHandle{
				Name_:      sbSlug + "-opencode",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now(),
			},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	got := map[string]bool{}
	for _, candidate := range candidates {
		got[candidate.Slug] = true
	}
	for _, slug := range []string{volSlug, imgSlug, sbSlug} {
		if !got[slug] {
			t.Errorf("slug %q missing from candidates %v; a non-matching artifact ended its list early", slug, got)
		}
	}
}

// TestEnumerateImageOnlyCandidateRecordsKeyAndCreatedActivity pins that a slug
// whose only trace is a runner image surfaces with the image's key and, when the
// image has never been used, derives its activity from the image's creation
// time.
func TestEnumerateImageOnlyCandidateRecordsKeyAndCreatedActivity(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "imgonly-1mjusbm3wikhb0"
	created := time.Now().Add(-48 * time.Hour)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: naming.RunnerImageRef(slug, "opencode"), CreatedAt_: created},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", candidates)
	}
	got := candidates[0]
	if want := []state.Key{{Slug: slug, Agent: "opencode"}}; !slices.Equal(got.Keys, want) {
		t.Errorf("Keys = %v, want the image's key %v", got.Keys, want)
	}
	if got.NewestActivity.Sub(created).Abs() > time.Second {
		t.Errorf("NewestActivity = %v, want the image's creation time %v", got.NewestActivity, created)
	}
}

// TestEnumerateSandboxOnlyCandidateRecordsKeyActivityAndKept pins that a slug
// whose only trace is a project VM surfaces with the VM's key, uses its
// UpdatedAt as activity, and — because a running VM is not prunable — is marked
// as having a kept sandbox.
func TestEnumerateSandboxOnlyCandidateRecordsKeyActivityAndKept(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "sbonly-1mjusbm3wikhb0"
	updated := time.Now().Add(-48 * time.Hour)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      slug + "-opencode",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: updated,
			},
		},
	})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", candidates)
	}
	got := candidates[0]
	if want := []state.Key{{Slug: slug, Agent: "opencode"}}; !slices.Equal(got.Keys, want) {
		t.Errorf("Keys = %v, want the VM's key %v", got.Keys, want)
	}
	if got.NewestActivity.Sub(updated).Abs() > time.Second {
		t.Errorf("NewestActivity = %v, want the VM's UpdatedAt %v", got.NewestActivity, updated)
	}
	if !got.HasKeptSandbox {
		t.Error("a running (non-prunable) project VM must set HasKeptSandbox")
	}
	if got.Retired(time.Now(), 7*24*time.Hour) {
		t.Error("a kept sandbox must block retirement")
	}
}

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

// TestEnumerateCandidatesIncludesAdoptionClaims pins the root-state source: a
// legacy identity's adoption claim surfaces as a candidate even when no state
// directory, volume, image or sandbox remains.
func TestEnumerateCandidatesIncludesAdoptionClaims(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "adopted-1mjusbm3wikhb0"
	seedAdoptionClaim(t, slug, "opencode", "checkout-slug", "legacy-volume")
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	candidates, err := EnumerateCandidates(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EnumerateCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", candidates)
	}
	if got := candidates[0].Keys; !slices.Equal(got, []state.Key{{Slug: slug, Agent: "opencode"}}) {
		t.Errorf("Keys = %v, want the adoption claim's key", got)
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
		if err := enumerateVolumeCandidates(ctx, emptyBuilderFor); !errors.Is(err, errBoom) {
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

// TestEnumerateAdoptionClaimsEdgeCases pins the root-state read failure and the
// claim key that carries no separator: a read failure fails the enumeration, a
// key without "/" is skipped rather than fabricating a slug.
func TestEnumerateAdoptionClaimsEdgeCases(t *testing.T) {
	t.Run("read error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		root := configpaths.Get().UserStateDir()
		if err := os.WriteFile(filepath.Join(root, "state.yaml"), []byte("schema: [not-valid"), 0o600); err != nil {
			t.Fatalf("corrupt root state: %v", err)
		}
		if err := enumerateAdoptionClaims(emptyBuilderFor); err == nil {
			t.Fatal("expected the root-state read error")
		}
	})

	t.Run("key without separator", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		_, err := state.WithRootState(true, func(txn *state.RootTxn) error {
			txn.State().AdoptedHomeVolumes = map[string]state.AdoptedHomeVolume{
				"no-separator": {CheckoutSlug: "checkout", Volume: "volume"},
			}
			return txn.Commit()
		})
		if err != nil {
			t.Fatalf("seed malformed claim: %v", err)
		}
		var seen []string
		collect := func(slug string) *candidateBuilder {
			seen = append(seen, slug)
			return &candidateBuilder{keys: map[state.Key]struct{}{}}
		}
		if err := enumerateAdoptionClaims(collect); err != nil {
			t.Fatalf("enumerateAdoptionClaims: %v", err)
		}
		if len(seen) != 0 {
			t.Errorf("builder slugs = %v, want none for a key without a separator", seen)
		}
	})
}

package pruning

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

var errBoom = errors.New("boom")

func TestBuildPruneState(t *testing.T) {
	recent := time.Now().Add(-5 * time.Minute)
	stale := time.Now().Add(-15 * 24 * time.Hour)

	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// running -> never stale (kept)
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj1-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: recent,
			},
			// stopped but fresh -> not yet stale (kept)
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj2-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recent,
			},
			// stale stopped -> prunable
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj3-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: stale,
			},
			// task sandbox -> prunable regardless of age
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-task-fill-1mjusbm3wikhb0-opencode-1729",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recent,
			},
			// running task -> never stale
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-task-fill2-1mjusbm3wikhb0-opencode-1729",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: stale,
			},
			// starting -> never stale while a runtime transition is in progress
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj4-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStarting,
				UpdatedAt_: stale,
			},
			// unknown -> never prunable; fail closed for future statuses
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj5-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatus("future-status"),
				UpdatedAt_: stale,
			},
		},
	}
	msb.WithMsbMock(t, client)

	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}]; ok {
		t.Error("running VM must not be prunable")
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}]; ok {
		t.Error("fresh stopped VM must not be prunable")
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj3-1mjusbm3wikhb0", Agent: ""}]; !ok {
		t.Error("stale stopped VM must be prunable")
	}
	// Task sandboxes are kept out of the key maps and land in Tasks so a
	// leftover task can never mark its project stale.
	if len(pruneState.Tasks) != 1 ||
		pruneState.Tasks[0].Name() != "agents-sandbox-task-fill-1mjusbm3wikhb0-opencode-1729" {
		t.Errorf("Tasks = %v, want exactly the stopped task sandbox", pruneState.Tasks)
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "fill-1mjusbm3wikhb0", Agent: "opencode"}]; ok {
		t.Error("a task sandbox must not enter ToPrune")
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj4-1mjusbm3wikhb0", Agent: ""}]; ok {
		t.Error("starting VM must not be prunable")
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj5-1mjusbm3wikhb0", Agent: ""}]; ok {
		t.Error("unknown-status VM must not be prunable")
	}
	// Kept VMs: live project VMs (running or not-yet-stale) are kept; a pruned VM
	// and task sandboxes never count as kept.
	if _, ok := pruneState.ToKeep[state.Key{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}]; !ok {
		t.Error("running VM must be a kept VM")
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}]; !ok {
		t.Error("fresh stopped VM must be a kept VM")
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "proj3-1mjusbm3wikhb0", Agent: ""}]; ok {
		t.Error("stale stopped VM must not be a kept VM")
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "fill-1mjusbm3wikhb0", Agent: "opencode"}]; ok {
		t.Error("task sandbox must not be a kept VM")
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "fill2-1mjusbm3wikhb0", Agent: "opencode"}]; ok {
		t.Error("running task sandbox must not be a kept VM")
	}
}

// TestPruneStateKeepsTaskOutOfProjectKey pins the regression Greptile found: a
// leftover stopped task sandbox for a live project must not mark that project
// stale. The task goes to Tasks; the running project VM stays in ToKeep and the
// project key never appears in ToPrune.
func TestPruneStateKeepsTaskOutOfProjectKey(t *testing.T) {
	const slug = "myproj-1mjusbm3wikhb0"
	task := &msb.MockSandboxHandle{
		Name_:      slug + "-opencode-1729000000000000000",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now(),
	}
	project := &msb.MockSandboxHandle{
		Name_:      slug + "-opencode",
		Status_:    msbSdk.SandboxStatusRunning,
		UpdatedAt_: time.Now(),
	}
	client := &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{task, project}}
	msb.WithMsbMock(t, client)

	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	key := state.Key{Slug: slug, Agent: "opencode"}
	if got := pruneState.ToKeep[key]; got != project {
		t.Errorf("ToKeep[%v] = %v, want the project VM", key, got)
	}
	if _, ok := pruneState.ToPrune[key]; ok {
		t.Error("a leftover task must not put its live project's key in ToPrune")
	}
	if len(pruneState.Tasks) != 1 || pruneState.Tasks[0] != task {
		t.Errorf("Tasks = %v, want [the task sandbox]", pruneState.Tasks)
	}
}

// TestPruneStateLabelBeatsLegacyNamePrefix pins the label-first classification:
// a project VM whose folder begins with the legacy task prefix is not a task
// when it carries the project type label, and a labelled task is a task.
func TestPruneStateLabelBeatsLegacyNamePrefix(t *testing.T) {
	project := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-task-1mjusbm3wikhb0-opencode",
		Status_:    msbSdk.SandboxStatusRunning,
		UpdatedAt_: time.Now(),
		Cfg: &msbSdk.SandboxConfig{Labels: map[string]string{
			naming.LabelType:  naming.SandboxTypeProject,
			naming.LabelSlug:  "agents-sandbox-task-1mjusbm3wikhb0",
			naming.LabelAgent: "opencode",
		}},
	}
	task := &msb.MockSandboxHandle{
		Name_:      "x-1mjusbm3wikhb0-opencode-1729000000000000000",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now(),
		Cfg: &msbSdk.SandboxConfig{Labels: map[string]string{
			naming.LabelType:  naming.SandboxTypeTask,
			naming.LabelSlug:  "x-1mjusbm3wikhb0",
			naming.LabelAgent: "opencode",
		}},
	}
	client := &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{project, task}}
	msb.WithMsbMock(t, client)

	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "agents-sandbox-task-1mjusbm3wikhb0", Agent: "opencode"}]; !ok {
		t.Errorf(
			"a project labelled project must be kept, got ToKeep=%v ToPrune=%v",
			pruneState.ToKeep,
			pruneState.ToPrune,
		)
	}
	if len(pruneState.Tasks) != 1 || pruneState.Tasks[0] != task {
		t.Errorf("Tasks = %v, want [the labelled task]", pruneState.Tasks)
	}
}

func TestBuildPruneState_AgeZero(t *testing.T) {
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now(),
			},
		},
	}
	msb.WithMsbMock(t, client)

	pruneState, err := buildPruneState(context.Background(), 0)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: ""}]; !ok {
		t.Error("age 0 must make any stopped VM prunable")
	}
}

// TestBuildPruneStateRecordsRetainKeys pins that the current invocation's keys
// enter the snapshot with the VM classification, so every liveness input is
// built in one place.
func TestBuildPruneStateRecordsRetainKeys(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}

	pruneState, err := buildPruneState(context.Background(), time.Hour, key)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if _, retained := pruneState.RetainKeys[key]; !retained || len(pruneState.RetainKeys) != 1 {
		t.Errorf("RetainKeys = %v, want exactly %v", pruneState.RetainKeys, key)
	}
}

// TestKeepImagePrecedence pins keepImage's first-match-wins order from
// specs/lifecycle.md (kept-VM ref → RetainKeys → ToPrune → agent within age →
// reclaim): each case sets up two conflicting rules and expects the earlier one
// to decide, so reordering the checks fails a case.
func TestKeepImagePrecedence(t *testing.T) {
	const ref = "proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31"
	const agentlessRef = "proj-1mjusbm3wikhb0-latest"
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	retiredKey := map[state.Key]msb.SandboxHandle{key: &msb.MockSandboxHandle{}}
	recent := time.Now().Add(-time.Minute)
	aged := time.Now().Add(-48 * time.Hour)

	for _, tc := range []struct {
		name         string
		reference    string
		lastUsedAt   time.Time
		heldByKeptVM bool
		retained     bool
		retired      bool
		want         bool
	}{
		{name: "kept-VM ref beats a retired key and age", reference: ref, lastUsedAt: aged, heldByKeptVM: true, retired: true, want: true},
		{name: "kept-VM ref beats a missing agent", reference: agentlessRef, lastUsedAt: aged, heldByKeptVM: true, want: true},
		{name: "retained key beats a retired key and age", reference: ref, lastUsedAt: aged, retained: true, retired: true, want: true},
		{name: "retired key beats recent use", reference: ref, lastUsedAt: recent, retired: true, want: false},
		{name: "recent use keeps an agent ref of a VM-less key", reference: ref, lastUsedAt: recent, want: true},
		{name: "recent use does not keep an agent-less ref", reference: agentlessRef, lastUsedAt: recent, want: false},
		{name: "nothing retains an aged ref", reference: ref, lastUsedAt: aged, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pruneState := PruneState{Age: time.Hour}
			if tc.retained {
				pruneState.RetainKeys = map[state.Key]struct{}{key: {}}
			}
			if tc.retired {
				pruneState.ToPrune = retiredKey
			}
			var keptImageRefs map[string]struct{}
			if tc.heldByKeptVM {
				keptImageRefs = map[string]struct{}{tc.reference: {}}
			}
			handle := &msb.MockImageHandle{Reference_: tc.reference, LastUsedAt_: tc.lastUsedAt}

			if got := keepImage(handle, naming.ParseImageTag(tc.reference), pruneState, keptImageRefs); got != tc.want {
				t.Errorf("keepImage = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildPruneStateRecordsAge(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	pruneState, err := buildPruneState(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if pruneState.Age != 24*time.Hour {
		t.Errorf("Age = %v, want the prune threshold carried for image retention", pruneState.Age)
	}

	// The threshold is carried even when the snapshot fails, so a caller that
	// reports the error still sees the age it asked for.
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ListSandboxesFn: func(_ context.Context, _ map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errBoom
		},
	})
	pruneState, err = buildPruneState(context.Background(), 7*24*time.Hour)
	if !errors.Is(err, errBoom) {
		t.Fatalf("buildPruneState error = %v, want %v", err, errBoom)
	}
	if pruneState.Age != 7*24*time.Hour {
		t.Errorf("Age on the error return = %v, want the prune threshold", pruneState.Age)
	}
}

func TestBuildPruneState_SkipsUnparseableName(t *testing.T) {
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// Vm-prefixed but with no slug remainder -> must be skipped.
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
			},
		},
	}
	msb.WithMsbMock(t, client)
	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if len(pruneState.ToPrune)+len(pruneState.ToKeep) != 0 {
		t.Errorf("unparseable sandbox must not appear in PruneState, got %v", pruneState)
	}
}

func TestBuildPruneState_ListError(t *testing.T) {
	client := &msb.MockMsbClient{
		ListSandboxesFn: func(_ context.Context, _ map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errBoom
		},
	}
	msb.WithMsbMock(t, client)
	_, err := buildPruneState(context.Background(), time.Hour)
	if err == nil {
		t.Fatal("expected error from ListSandboxes")
	}
}

// TestNoteIdentityPrefersLabelsOverName pins that a sandbox carrying the
// launcher's identity labels is classified from them, not from its name: the
// name below parses to a different slug and agent.
func TestNoteIdentityPrefersLabelsOverName(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Name_: "unparseable-name",
		Cfg: &msbSdk.SandboxConfig{Labels: map[string]string{
			naming.LabelType:  naming.SandboxTypeProject,
			naming.LabelSlug:  "labeled-1mjusbm3wikhb0",
			naming.LabelAgent: "opencode",
		}},
	}
	want := naming.ArtifactInfo{Slug: "labeled-1mjusbm3wikhb0", Agent: "opencode"}
	if got := noteIdentity(handle); got != want {
		t.Errorf("noteIdentity() = %+v, want %+v", got, want)
	}
}

// TestNoteIdentityFallsBackOnConfigError pins that a config read error wins over
// a populated config: a handle that returns both labels and an error must use
// the name heuristic, not the labels.
func TestNoteIdentityFallsBackOnConfigError(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Name_: "unparseable-name",
		Cfg: &msbSdk.SandboxConfig{Labels: map[string]string{
			naming.LabelType:  naming.SandboxTypeProject,
			naming.LabelSlug:  "labeled-1mjusbm3wikhb0",
			naming.LabelAgent: "opencode",
		}},
		ConfigErr: errBoom,
	}
	want := naming.SandboxIdentity(handle.Name(), nil)
	if got := noteIdentity(handle); got != want {
		t.Errorf("noteIdentity() = %+v, want the name-derived %+v", got, want)
	}
}

// TestKeySetReturnsNilForNoKeys pins keySet's nil-for-empty contract: a caller
// assigns the result straight into PruneState.RetainKeys, where nil is the
// "retain nothing" signal, so an empty input must not yield an empty map.
func TestKeySetReturnsNilForNoKeys(t *testing.T) {
	if got := keySet(nil); got != nil {
		t.Errorf("keySet(nil) = %v, want nil", got)
	}
	if got := keySet([]state.Key{}); got != nil {
		t.Errorf("keySet(empty) = %v, want nil", got)
	}

	keys := []state.Key{{Slug: "slug-1", Agent: "opencode"}}
	got := keySet(keys)
	if len(got) != 1 {
		t.Fatalf("keySet(%v) has %d entries, want 1", keys, len(got))
	}
	if _, ok := got[keys[0]]; !ok {
		t.Errorf("keySet(%v) = %v, want it to index the key", keys, got)
	}
}

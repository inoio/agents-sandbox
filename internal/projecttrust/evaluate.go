package projecttrust

import (
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// ChangeStatus names how an input differs from the approved projection.
type ChangeStatus string

// The change statuses.
const (
	StatusAdded    ChangeStatus = "new"
	StatusRemoved  ChangeStatus = "removed"
	StatusModified ChangeStatus = "changed"
)

// reorderedNote is shown for a modified input whose entries only moved.
const reorderedNote = "(lines reordered)"

// Change is one input that differs from the approved projection, with its
// differing entries as "- old" and "+ new" lines.
type Change struct {
	Path   string
	Kind   InputKind
	Status ChangeStatus
	Lines  []string
}

// Decision is the gate's verdict on the current projection.
type Decision struct {
	Approved bool
	Changes  []Change
	Current  Projection
}

// Evaluate compares the current projection with the recorded approval. An empty
// projection and one matching the approved digest are approved; otherwise every
// added, removed, and modified input is a change, sorted by path. Without a
// record every input is new.
func Evaluate(current Projection, approved state.ApprovedProjectLayer, recorded bool) Decision {
	if current.Empty() || (recorded && approved.Digest == current.Digest()) {
		return Decision{Approved: true, Changes: nil, Current: current}
	}
	previous := map[string]state.TrustedInput{}
	for _, input := range approved.Inputs {
		previous[input.Path] = input
	}
	var changes []Change
	for _, input := range current.Inputs {
		before, existed := previous[input.Path]
		delete(previous, input.Path)
		switch {
		case !existed:
			changes = append(changes, Change{
				Path: input.Path, Kind: input.Kind, Status: StatusAdded, Lines: prefixed("+ ", input.Entries),
			})
		case before.Kind != string(input.Kind) || !slices.Equal(before.Entries, input.Entries):
			changes = append(changes, Change{
				Path:   input.Path,
				Kind:   input.Kind,
				Status: StatusModified,
				Lines:  entryDiff(before.Entries, input.Entries),
			})
		}
	}
	for _, input := range previous {
		changes = append(changes, Change{
			Path: input.Path, Kind: InputKind(input.Kind), Status: StatusRemoved, Lines: prefixed("- ", input.Entries),
		})
	}
	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return Decision{Approved: false, Changes: changes, Current: current}
}

// entryDiff lists the entries only in before as "- entry", then those only in
// after as "+ entry"; entries that only moved yield the reorder note.
func entryDiff(before, after []string) []string {
	var lines []string
	for _, entry := range before {
		if !slices.Contains(after, entry) {
			lines = append(lines, "- "+entry)
		}
	}
	for _, entry := range after {
		if !slices.Contains(before, entry) {
			lines = append(lines, "+ "+entry)
		}
	}
	if len(lines) == 0 {
		return []string{reorderedNote}
	}
	return lines
}

func prefixed(prefix string, entries []string) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, prefix+entry)
	}
	return lines
}

// fromApproval converts a recorded approval back into the projection it approved.
func fromApproval(approval state.ApprovedProjectLayer) Projection {
	inputs := make([]Input, 0, len(approval.Inputs))
	for _, input := range approval.Inputs {
		inputs = append(inputs, Input{Path: input.Path, Kind: InputKind(input.Kind), Entries: input.Entries})
	}
	return Projection{Inputs: inputs}
}

// toApproval converts a projection into the record that approves it.
func toApproval(projection Projection) state.ApprovedProjectLayer {
	inputs := make([]state.TrustedInput, 0, len(projection.Inputs))
	for _, input := range projection.Inputs {
		inputs = append(inputs, state.TrustedInput{Path: input.Path, Kind: string(input.Kind), Entries: input.Entries})
	}
	return state.ApprovedProjectLayer{Digest: projection.Digest(), Inputs: inputs}
}

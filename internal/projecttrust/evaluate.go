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

// maxDiffCells bounds the line diff's table; inputs whose line counts multiply
// past it are shown as the whole old and new content.
const maxDiffCells = 1_000_000

// Change is one input that differs from the approved projection, with its
// differing entries as "- old" and "+ new" lines in order.
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

// Evaluate compares the current projection with the recorded approval (the zero
// value when there is none). An empty projection and one matching the approved
// digest are approved; otherwise every added, removed, and modified input is a
// change, sorted by path and kind.
func Evaluate(current Projection, approved state.ApprovedProjectLayer) Decision {
	if current.Empty() || approved.Digest == current.Digest() {
		return Decision{Approved: true, Changes: nil, Current: current}
	}
	previous := map[string]Input{}
	for _, input := range fromApproval(approved).Inputs {
		previous[input.key()] = input
	}
	var changes []Change
	for _, input := range current.Inputs {
		before, existed := previous[input.key()]
		delete(previous, input.key())
		switch {
		case !existed:
			changes = append(changes, Change{
				Path: input.Path, Kind: input.Kind, Status: StatusAdded, Lines: prefixed("+ ", input.Entries),
			})
		case !slices.Equal(before.Entries, input.Entries):
			changes = append(changes, Change{
				Path:   input.Path,
				Kind:   input.Kind,
				Status: StatusModified,
				Lines:  lineDiff(before.Entries, input.Entries),
			})
		}
	}
	for _, input := range previous {
		changes = append(changes, Change{
			Path: input.Path, Kind: input.Kind, Status: StatusRemoved, Lines: prefixed("- ", input.Entries),
		})
	}
	slices.SortFunc(changes, func(a, b Change) int {
		return strings.Compare(a.Path+"\x00"+string(a.Kind), b.Path+"\x00"+string(b.Kind))
	})
	return Decision{Approved: false, Changes: changes, Current: current}
}

// lineDiff lists the removed entries as "- entry" and the added ones as
// "+ entry", in order, from a longest common subsequence, so a moved or repeated
// line shows. Inputs too long to diff are shown whole.
func lineDiff(before, after []string) []string {
	if len(before)*len(after) > maxDiffCells {
		return append(prefixed("- ", before), prefixed("+ ", after)...)
	}
	// common[i][j] is the length of the longest common subsequence of
	// before[i:] and after[j:].
	common := make([][]int, len(before)+1)
	for i := range common {
		common[i] = make([]int, len(after)+1)
	}
	for i, beforeLine := range slices.Backward(before) {
		for j, afterLine := range slices.Backward(after) {
			if beforeLine == afterLine {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	var lines []string
	i, j := 0, 0
	for i < len(before) && j < len(after) {
		switch {
		case before[i] == after[j]:
			i++
			j++
		case common[i+1][j] >= common[i][j+1]:
			lines = append(lines, "- "+before[i])
			i++
		default:
			lines = append(lines, "+ "+after[j])
			j++
		}
	}
	lines = append(lines, prefixed("- ", before[i:])...)
	return append(lines, prefixed("+ ", after[j:])...)
}

func prefixed(prefix string, entries []string) []string {
	var lines []string
	for _, entry := range entries {
		lines = append(lines, prefix+entry)
	}
	return lines
}

// fromApproval converts a recorded approval back into the projection it approved.
func fromApproval(approval state.ApprovedProjectLayer) Projection {
	var inputs []Input
	for _, input := range approval.Inputs {
		inputs = append(inputs, Input{Path: input.Path, Kind: InputKind(input.Kind), Entries: input.Entries})
	}
	return Projection{Inputs: inputs}
}

// toApproval converts a projection into the record that approves it.
func toApproval(projection Projection) state.ApprovedProjectLayer {
	var inputs []state.TrustedInput
	for _, input := range projection.Inputs {
		inputs = append(inputs, state.TrustedInput{Path: input.Path, Kind: string(input.Kind), Entries: input.Entries})
	}
	return state.ApprovedProjectLayer{Digest: projection.Digest(), Inputs: inputs}
}

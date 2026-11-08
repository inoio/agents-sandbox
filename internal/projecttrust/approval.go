package projecttrust

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// ErrDeclined reports that the user declined a project config change; it is a
// termio.ErrCancelled.
var ErrDeclined = fmt.Errorf("%w: project config change not approved", termio.ErrCancelled)

// ErrApprovalRequired reports that a project config change needs approval but
// the run cannot ask for it.
var ErrApprovalRequired = errors.New("project config change requires approval")

const (
	approveKey = "a"
	declineKey = "q"
)

// Approve asks the user to approve a changed project config layer and records
// the approval. Only an interactive answer approves: without a terminal it
// returns ErrApprovalRequired, and it asks through Select, which assume-yes
// does not answer. A declined change returns ErrDeclined.
func Approve(ui termio.Prompter, checkoutSlug string, decision Decision) error {
	if decision.Approved {
		return nil
	}
	if !ui.IsInteractive() {
		return fmt.Errorf("%w: %s\nRun agents-sandbox interactively in this checkout to review and approve it",
			ErrApprovalRequired, changedPaths(decision.Changes))
	}
	prompt := RenderChanges(decision.Changes) +
		"\nThe agent can edit these files. Approve them only if you expect these changes."
	key, err := ui.Select(prompt, []termio.Choice{
		{Key: approveKey, Label: "Approve", Description: "use this project config for this checkout"},
		{Key: declineKey, Label: "Quit", Description: "do not use it; nothing is changed"},
	}, declineKey)
	if err != nil {
		return fmt.Errorf("ask for project config approval: %w", err)
	}
	if key != approveKey {
		return ErrDeclined
	}
	return RecordApproval(checkoutSlug, decision.Current)
}

// RenderChanges lists the changed inputs with their differing entries, every
// value escaped for the terminal.
func RenderChanges(changes []Change) string {
	lines := []string{"The project config (.agents-sandbox) changed since you last approved it:"}
	for _, change := range changes {
		lines = append(lines, fmt.Sprintf("  %s %s (%s):",
			change.Status, termio.EscapeControl(change.Path), change.Kind))
		for _, line := range change.Lines {
			lines = append(lines, "      "+termio.EscapeControl(line))
		}
	}
	return strings.Join(lines, "\n")
}

// IgnoredWarning is the notice for a command that runs without an unapproved
// project config layer.
func IgnoredWarning(decision Decision) string {
	return fmt.Sprintf("the project config changed and is not approved, so it is ignored for this command: %s\n"+
		"Run agents-sandbox interactively in this checkout to review and approve it", changedPaths(decision.Changes))
}

func changedPaths(changes []Change) string {
	var paths []string
	for _, change := range changes {
		paths = append(paths, termio.EscapeControl(change.Path))
	}
	return strings.Join(paths, ", ")
}

// LoadApproval returns the approval recorded for a checkout.
func LoadApproval(checkoutSlug string) (state.ApprovedProjectLayer, bool, error) {
	root, err := state.ReadRootState()
	if err != nil {
		return state.ApprovedProjectLayer{}, false, fmt.Errorf("read project config approval: %w", err)
	}
	approval, found := root.ApprovedProjectLayer(checkoutSlug)
	return approval, found, nil
}

// RecordApproval records projection as the checkout's approved project config
// layer.
func RecordApproval(checkoutSlug string, projection Projection) error {
	_, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		txn.State().ApproveProjectLayer(checkoutSlug, toApproval(projection))
		return txn.Commit()
	})
	if err != nil {
		return fmt.Errorf("record project config approval: %w", err)
	}
	return nil
}

// ApproveSettingWrite extends the checkout's approval by a setting the launcher
// is about to write to the project launcher config file, so the next run does
// not ask about the launcher's own change. Only that key's entry
// is updated: anything else that differs in the file still needs approval.
// Without an approval there is nothing to extend, and nothing is recorded.
//
// The approval is recorded before the write: a write without its approval would
// make the next run ask about the launcher's own change. The returned function
// restores the previous approval for a write that then fails; should that
// restore fail too, the approval is ahead of the file, which only makes the next
// run ask again.
func ApproveSettingWrite(checkoutSlug, key, value string) (func(), error) {
	var previous state.ApprovedProjectLayer
	var found bool
	_, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		previous, found = txn.State().ApprovedProjectLayer(checkoutSlug)
		if !found {
			return nil
		}
		projection := fromApproval(previous)
		setSetting(projection.Inputs, key, value)
		txn.State().ApproveProjectLayer(checkoutSlug, toApproval(projection))
		return txn.Commit()
	})
	if err != nil {
		return nil, fmt.Errorf("record the approval of %s: %w", key, err)
	}
	return func() {
		if !found {
			return
		}
		_, _ = state.WithRootState(true, func(txn *state.RootTxn) error {
			txn.State().ApproveProjectLayer(checkoutSlug, previous)
			return txn.Commit()
		})
	}, nil
}

// setSetting sets key to value in the launcher config input. The launcher
// writes only a key its project config already sets, so that input exists;
// without it the approval stays as it is, and the next run asks.
func setSetting(inputs []Input, key, value string) {
	encoded, _ := json.Marshal(value)
	for i, input := range inputs {
		if input.Kind != KindLauncherConfig {
			continue
		}
		entries := slices.DeleteFunc(slices.Clone(input.Entries), func(existing string) bool {
			return strings.HasPrefix(existing, key+": ")
		})
		entries = append(entries, key+": "+string(encoded))
		slices.Sort(entries)
		inputs[i].Entries = entries
	}
}

package projecttrust

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// ErrDeclined reports that the user declined a project config approval; it is a
// termio.ErrCancelled.
var ErrDeclined = fmt.Errorf("%w: project config not approved", termio.ErrCancelled)

// ErrApprovalRequired reports that a project config needs approval but the run
// cannot ask for it.
var ErrApprovalRequired = errors.New("project config requires approval")

const (
	approveKey = "a"
	declineKey = "q"
)

// Approve asks the user to approve a changed project config layer and records
// the approval. Only an interactive answer approves: without a terminal it
// returns ErrApprovalRequired, and it asks through Select, which assume-yes
// does not answer. A declined prompt returns ErrDeclined.
func Approve(ui termio.Prompter, checkoutSlug string, decision Decision) error {
	if decision.Approved {
		return nil
	}
	wording := wordingFor(decision.FirstApproval)
	if !ui.IsInteractive() {
		return fmt.Errorf("%w: %s\n%s", ErrApprovalRequired, changedPaths(decision.Changes), wording.remedy)
	}
	prompt := RenderChanges(decision) + "\nThe agent can edit these files. " + wording.trailer
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

// RenderChanges renders the decision's changed inputs with their differing
// entries, every value escaped for the terminal. The headline reads as new
// configuration for a first approval, and as a change since the last approved
// projection otherwise.
func RenderChanges(decision Decision) string {
	lines := []string{wordingFor(decision.FirstApproval).headline}
	for _, change := range decision.Changes {
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
	wording := wordingFor(decision.FirstApproval)
	return fmt.Sprintf("%s, so it is ignored for this command: %s\n%s",
		wording.reason, changedPaths(decision.Changes), wording.remedy)
}

// approvalWording is the phrasing of every approval surface, chosen once by
// whether this is a first approval or a change since the last one. A new
// phrasing state is one edit here, not one per surface.
type approvalWording struct {
	headline string // introduces the change list
	trailer  string // advises how to answer the prompt
	reason   string // why the layer is ignored or the run refuses
	remedy   string // how to resolve it
}

// wordingFor returns the phrasing for a first approval or a change.
func wordingFor(firstApproval bool) approvalWording {
	if firstApproval {
		return approvalWording{
			headline: "The project config (.agents-sandbox) is not approved for this checkout yet. Approve this new configuration:",
			trailer:  "Approve this new configuration only if you expect it.",
			reason:   "the project config is not approved yet",
			remedy:   "This checkout has no approved project config yet. Run agents-sandbox interactively in this checkout to review and approve it",
		}
	}
	return approvalWording{
		headline: "The project config (.agents-sandbox) changed since you last approved it:",
		trailer:  "Approve them only if you expect these changes.",
		reason:   "the project config changed and is not approved",
		remedy:   "Run agents-sandbox interactively in this checkout to review and approve it",
	}
}

func changedPaths(changes []Change) string {
	// One file can carry several inputs (config.yaml's launcher settings and
	// its home: manifest), so each path is named once.
	var paths []string
	for _, change := range changes {
		if path := termio.EscapeControl(change.Path); !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
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
func ApproveSettingWrite(checkoutSlug, key string, value any) (func(), error) {
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

// setSetting sets key to value in the launcher config input, recorded exactly as
// the projection renders it: the value is JSON-encoded here and nowhere else, so
// a boolean or list entry matches what `appendSettingEntries` computes. The
// launcher writes only a key its project config already sets, so that input
// exists; without it the approval stays as it is, and the next run asks.
func setSetting(inputs []Input, key string, value any) {
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

// ApproveSecretWrite extends the checkout's approval by a secret the launcher is
// about to remove from the project secret file, so the next run does not ask
// about the launcher's own change. Only that env-var's entry is removed:
// anything else that differs in the file still needs approval. Without an
// approval there is nothing to extend, and nothing is recorded.
//
// The approval is recorded before the write: a write without its approval would
// make the next run ask about the launcher's own change. The returned function
// restores the previous approval for a write that then fails; should that
// restore fail too, the approval is ahead of the file, which only makes the next
// run ask again.
func ApproveSecretWrite(checkoutSlug, envVar string) (func(), error) {
	var previous state.ApprovedProjectLayer
	var found bool
	_, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		previous, found = txn.State().ApprovedProjectLayer(checkoutSlug)
		if !found {
			return nil
		}
		projection := fromApproval(previous)
		projection.Inputs = withoutSecretEntry(projection.Inputs, envVar)
		txn.State().ApproveProjectLayer(checkoutSlug, toApproval(projection))
		return txn.Commit()
	})
	if err != nil {
		return nil, fmt.Errorf("record the approval of secret %s: %w", envVar, err)
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

// withoutSecretEntry removes envVar's entry from the structured project secret
// file's input and drops an input left with no entries, so the recorded approval
// matches the projection the file yields once the entry is gone. Only
// env.secret.yaml is extended: the legacy secret file the writer does not touch
// keeps its recorded entries.
func withoutSecretEntry(inputs []Input, envVar string) []Input {
	secretFile := configpaths.EnvSecretFileName + ".yaml"
	result := make([]Input, 0, len(inputs))
	for _, input := range inputs {
		if input.Kind != KindSecrets || filepath.Base(input.Path) != secretFile {
			result = append(result, input)
			continue
		}
		entries := slices.DeleteFunc(slices.Clone(input.Entries), func(entry string) bool {
			return secretEntryNames(entry, envVar)
		})
		if len(entries) == 0 {
			continue
		}
		input.Entries = entries
		result = append(result, input)
	}
	return result
}

// secretEntryNames reports whether entry is envVar's rendering in a secret
// input's entries: the env-var name alone (a secret with no substitution scope)
// or the name before the scope parts describeSecretSpec appends.
func secretEntryNames(entry, envVar string) bool {
	return entry == envVar || strings.HasPrefix(entry, envVar+": ")
}

// RefreshSecretFileInput updates the recorded approval's raw-content input for
// dir's env.secret.yaml, if it has one, to the file's current bytes. A project
// secret file that is also a home: source is projected a second time by content
// digest, so the launcher's own edit must refresh that input too; without it the
// next run gates the launcher's own removal. Without an approval there is
// nothing to refresh.
func RefreshSecretFileInput(checkoutSlug, dir string) error {
	path := filepath.Join(dir, configpaths.EnvSecretFileName+".yaml")
	_, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		previous, _ := txn.State().ApprovedProjectLayer(checkoutSlug)
		projection := fromApproval(previous)
		refreshed, changed := refreshFileInput(projection.Inputs, path)
		if !changed {
			return nil
		}
		projection.Inputs = refreshed
		txn.State().ApproveProjectLayer(checkoutSlug, toApproval(projection))
		return txn.Commit()
	})
	if err != nil {
		return fmt.Errorf("refresh the approval of %s: %w", path, err)
	}
	return nil
}

// refreshFileInput rewrites path's raw-content input, if any, to the file's
// current bytes and reports whether it changed anything. Only a file reached
// both as a parsed input and as a home: source carries such an input, digest-only
// so its values stay out of the projection; a file that is no source has none.
func refreshFileInput(inputs []Input, path string) ([]Input, bool) {
	result := make([]Input, 0, len(inputs))
	changed := false
	for _, input := range inputs {
		if input.Kind != KindFile || !sameSourcePath(input.Path, path) {
			result = append(result, input)
			continue
		}
		fileInput, err := readFileInput(path)
		if err != nil {
			// The file is gone or unreadable; the projection adds no home:
			// input for it either.
			changed = true
			continue
		}
		input.Entries = []string{fileInput.Digest}
		input.Digest = fileInput.Digest
		result = append(result, input)
		changed = true
	}
	return result, changed
}

// sameSourcePath reports whether two spellings name the same file: the
// projection resolves the base of every path it records, so a caller's
// unresolved spelling must compare equal to the recorded one.
func sameSourcePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b) || resolvedPath(a) == resolvedPath(b)
}

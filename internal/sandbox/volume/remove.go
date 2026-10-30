package volume

import (
	"context"
	"fmt"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// RemoveVolumes removes the named private home volumes. Unless force is set, it
// refuses when any name is still referenced by a project state file, listing the
// referencing projects, so a project's recorded home is never silently orphaned.
// It prompts for confirmation after the reference guard passes.
func RemoveVolumes(ctx context.Context, names []string, force bool, ui termio.UI) error {
	if !force {
		references, err := state.HomeVolumeReferences()
		if err != nil {
			return fmt.Errorf("check volume references: %w", err)
		}
		if err := refuseReferencedVolumes(names, references); err != nil {
			return err
		}
	}
	if err := termio.ConfirmAction(ui, fmt.Sprintf("Remove %d home volume(s)?", len(names))); err != nil {
		return err
	}
	for _, name := range names {
		if err := msb.Get().RemoveVolume(ctx, name); err != nil {
			return fmt.Errorf("remove volume %q: %w", name, err)
		}
	}
	return nil
}

// refuseReferencedVolumes returns an error naming every requested volume that a
// project state file still records, so the caller can decide to force the
// removal explicitly.
func refuseReferencedVolumes(names []string, references map[string][]state.Key) error {
	var refused []string
	for _, name := range names {
		if keys := references[name]; len(keys) > 0 {
			refused = append(refused, fmt.Sprintf("%s (referenced by %s)", name, state.FormatKeys(keys)))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf(
		"refusing to remove volume(s) still referenced by a project: %s; "+
			"re-run with --force to remove anyway",
		strings.Join(refused, "; "),
	)
}

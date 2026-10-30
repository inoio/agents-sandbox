package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// RemoveImages removes the named runner image references. It first refuses any
// reference that is not an agents-sandbox runner image, then, unless force is
// set, refuses a reference that is still in use — recorded by a project state or
// held by a live project/task sandbox — naming the referencing projects, so an
// in-use image is never silently deleted. Both guards fail closed.
func RemoveImages(ctx context.Context, refs []string, force bool, ui termio.UI) error {
	if err := refuseNonRunnerRefs(refs); err != nil {
		return err
	}
	if !force {
		references, err := inUseImageReferences(ctx)
		if err != nil {
			return fmt.Errorf("check image references: %w", err)
		}
		if err := refuseReferencedImages(refs, references); err != nil {
			return err
		}
	}
	if err := termio.ConfirmAction(ui, fmt.Sprintf("Remove %d runner image(s)?", len(refs))); err != nil {
		return err
	}
	for _, ref := range refs {
		if err := msb.Get().ImageRemove(ctx, ref, force); err != nil {
			return fmt.Errorf("remove image %q: %w", ref, err)
		}
	}
	return nil
}

// refuseNonRunnerRefs fails closed on a reference that is not a runner image
// under either scheme. Ownership requires the parsed slug to embed the
// launcher's 14-character slug hash, so a manually loaded single-segment image
// in the private runtime home is foreign. Unlike the in-use guard, this
// ownership boundary is not overridable by --force: the command removes only
// the objects agents-sandbox builds, never arbitrary cached images.
func refuseNonRunnerRefs(refs []string) error {
	var foreign []string
	for _, ref := range refs {
		if !naming.IsRunnerImageRef(ref) {
			foreign = append(foreign, ref)
		}
	}
	if len(foreign) == 0 {
		return nil
	}
	return fmt.Errorf(
		"refusing to remove image(s) not managed by agents-sandbox: %s",
		strings.Join(foreign, ", "),
	)
}

// inUseImageReferences unions the project-state ownership scan with the image
// references held by the live launcher sandboxes, reusing the fail-closed
// Slice 1 seam. Ownership is positional: every sandbox in the private runtime
// home is ours, so a live VM or task worker holding an image protects it from
// removal.
func inUseImageReferences(ctx context.Context) (map[string][]state.Key, error) {
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		return nil, err
	}
	return InUseImageReferences(handles)
}

// refuseReferencedImages returns an error naming every requested reference that
// is still in use, so the caller can force the removal explicitly.
func refuseReferencedImages(refs []string, references map[string][]state.Key) error {
	var refused []string
	for _, ref := range refs {
		if keys := references[ref]; len(keys) > 0 {
			refused = append(refused, fmt.Sprintf("%s (referenced by %s)", ref, state.FormatKeys(keys)))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf(
		"refusing to remove image(s) still in use: %s; re-run with --force to remove anyway",
		strings.Join(refused, "; "),
	)
}

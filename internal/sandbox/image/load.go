package image

import (
	"context"
	"fmt"
	"slices"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// imageRefCandidates returns the microsandbox references that may already hold
// info's content, in reuse-preference order: the content ref, the prefix-free
// name ref a 0.6.0 install cached under, and the legacy prefixed Docker-tag ref a
// pre-0.6 install cached under. The name ref comes from the image's structured
// identity, so a reference the launcher rendered is never parsed back; the
// Docker tag is carried as-is. Empty and duplicate refs are dropped.
func imageRefCandidates(info ImageInfo) []string {
	var candidates []string
	for _, ref := range []string{info.Ref, info.Identity.NameRef(), info.DockerTag} {
		if ref == "" || slices.Contains(candidates, ref) {
			continue
		}
		candidates = append(candidates, ref)
	}
	return candidates
}

// EnsureLoaded loads the runner image described by info into the microsandbox
// cache if it is not already the cached content, so the image can be used to
// create VMs, and returns info with Ref set to the reference that now holds that
// content. It is idempotent: it reuses the first candidate reference whose cache
// content matches (see imageRefCandidates for the content, name, and legacy
// prefixed refs), otherwise it loads the Docker image under info.Ref. The
// candidate set is derived from info.Identity, so a reference the launcher
// rendered is never parsed back. Loading directly avoids removing a manifest
// that an existing VM may still reference.
func EnsureLoaded(
	ctx context.Context,
	dclient docker.Client,
	mclient msb.ImageStore,
	info ImageInfo,
	ui termio.UI,
) (ImageInfo, error) {
	anyCandidateCached := false
	for _, candidate := range imageRefCandidates(info) {
		if mclient.ImageGet(ctx, candidate) != nil {
			continue
		}
		anyCandidateCached = true
		if cachedCandidateMatches(ctx, mclient, candidate, info) {
			info.Ref = candidate
			ui.Verbosef("microsandbox image %s is up to date", candidate)
			return info, nil
		}
	}
	if info.Ref == "" {
		info.Ref = info.DockerTag
	}
	loadReason := "not present in microsandbox cache"
	if anyCandidateCached {
		loadReason = "cached content differs from the Docker image"
	}
	ui.Verbosef("loading image %s into microsandbox: %s", info.Ref, loadReason)

	spin := ui.Spinner("Loading image into microsandbox")
	saveResult, err := dclient.ImageSave(ctx, []string{info.DockerTag})
	if err != nil {
		spin.StopError(err)
		return ImageInfo{}, fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()
	if err = mclient.ImageLoad(ctx, info.Ref, saveResult); err != nil {
		spin.StopError(err)
		return ImageInfo{}, err
	}
	spin.Stop()
	return info, nil
}

// cachedCandidateMatches reports whether the cached image under candidate holds
// info's content. The content-addressed reference encodes the content id (the
// Docker image ID's short hex), so a cache hit on it proves the content and is
// trusted directly. The name and legacy references do not encode content, so
// their cached content is compared by digest (cachedImageMatches).
func cachedCandidateMatches(ctx context.Context, mclient msb.ImageStore, candidate string, info ImageInfo) bool {
	if info.Identity.ContentID != "" && candidate == info.Identity.Ref() {
		return true
	}
	return cachedImageMatches(ctx, mclient, candidate, info)
}

// cachedImageMatches reports whether the microsandbox image under ref holds the
// same content as the Docker image described by info, comparing the digest
// microsandbox records against Docker's image ID. It guards only the candidate
// references that do not encode content (the name and legacy refs), and it is
// best-effort: the runtime's recorded digest is not guaranteed to equal Docker's
// image ID, so a mismatch there is not proof the bytes differ. The
// content-addressed reference is never checked this way, because its content id
// already identifies the image (see cachedCandidateMatches). A nil inspect
// result, or one that records no digest, is unknown content and never counts as
// a match: the dockerfile-id label is a source signature, not a content
// identity, and can match while the bytes differ (a floating base tag, or the
// dev user's uid/gid), so it must not stand in for the digest.
func cachedImageMatches(ctx context.Context, mclient msb.ImageStore, ref string, info ImageInfo) bool {
	mInspect, err := mclient.ImageInspect(ctx, ref)
	if err != nil {
		return false
	}
	return mInspect != nil && mInspect.Digest != "" && mInspect.Digest == info.Digest
}

package image

import (
	"context"
	"fmt"

	"github.com/distribution/reference"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// resolveBaseDigest returns "<ref>@sha256:<digest>" for the base image, pulling
// it first when not present locally, or always when refresh is set. The FROM
// itself stays a tag so it floats; the digest records what the image was built
// from and is compared with the locally cached base on later runs
// (changedHostReasons). The reference is pulled as given (the rendered FROM
// token, so the engine resolves build and lookup alike), but the returned label
// uses the familiar spelling, so the provenance label reads the way the user
// wrote the base.
//
// A failed refresh falls back to a locally cached base with a warning, so
// --rebuild still works offline. A failed pull is surfaced through
// buildFailureError: the engine delivers a registry-level failure both as the
// call error and as a message inside the stream (moby's Wait), and draining the
// stream without waiting would lose the latter and let the follow-up inspect
// report a misleading "No such image".
func resolveBaseDigest(
	ctx context.Context,
	dclient docker.Client,
	baseRef string,
	refresh bool,
	ui termio.Logger,
) (string, error) {
	if refresh {
		pullErr := pullBaseImage(ctx, dclient, baseRef)
		if pullErr == nil {
			return inspectPulledBase(ctx, dclient, baseRef)
		}
		local := localBaseImage(ctx, dclient, baseRef)
		if local == "" {
			return "", pullErr
		}
		ui.Warnf("could not refresh base image %s, building from the local copy: %v", baseRef, pullErr)
		return local, nil
	}
	if local := localBaseImage(ctx, dclient, baseRef); local != "" {
		return local, nil
	}
	ui.Verbosef("base image %s not present locally, pulling", baseRef)
	if err := pullBaseImage(ctx, dclient, baseRef); err != nil {
		return "", err
	}
	return inspectPulledBase(ctx, dclient, baseRef)
}

// localBaseImage returns the provenance value of the locally cached base image,
// or "" when it is not present. It never pulls.
func localBaseImage(ctx context.Context, dclient docker.Client, baseRef string) string {
	inspect, err := dclient.ImageInspect(ctx, baseRef)
	if err != nil || inspect.ID == "" {
		return ""
	}
	return baseProvenance(baseRef, inspect.ID)
}

// pullBaseImage pulls baseRef and waits for the pull to finish.
func pullBaseImage(ctx context.Context, dclient docker.Client, baseRef string) error {
	pull, err := dclient.ImagePull(ctx, baseRef, client.ImagePullOptions{})
	if err != nil {
		return buildFailureError(fmt.Errorf("pull base image %s: %w", baseRef, err))
	}
	waitErr := pull.Wait(ctx)
	_ = pull.Close()
	if waitErr != nil {
		return buildFailureError(fmt.Errorf("pull base image %s: %w", baseRef, waitErr))
	}
	return nil
}

// inspectPulledBase returns the provenance value of a just-pulled base image.
func inspectPulledBase(ctx context.Context, dclient docker.Client, baseRef string) (string, error) {
	inspect, err := dclient.ImageInspect(ctx, baseRef)
	if err != nil {
		return "", fmt.Errorf("inspect pulled base image %s: %w", baseRef, err)
	}
	return baseProvenance(baseRef, inspect.ID), nil
}

// baseProvenance renders the base label value "<familiar ref>@<image id>".
func baseProvenance(baseRef, imageID string) string {
	return familiarImageRef(baseRef) + "@" + imageID
}

// familiarImageRef strips the registry from a normalized image reference so the
// recorded base label keeps the spelling a user knows (debian:trixie-slim rather
// than docker.io/library/debian:trixie-slim).
func familiarImageRef(ref string) string {
	parsed, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ref
	}
	return reference.FamiliarString(parsed)
}

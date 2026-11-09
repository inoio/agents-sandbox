package image

import (
	"context"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// builderKind selects the engine builder path a runner-image build uses.
type builderKind int

const (
	builderClassic builderKind = iota
	builderBuildKit
)

// selectBuilder returns the builder the engine advertises via the ping
// Builder-Version header: the dockerd family advertises BuilderBuildKit ("2")
// and selects the BuildKit path; podman's compat /ping reports BuilderV1, and
// an absent header, both select the classic path. A ping failure surfaces
// unchanged: a build on an unreachable engine must not silently assume a
// builder.
func selectBuilder(ctx context.Context, dclient docker.Client) (builderKind, error) {
	ping, err := dclient.Ping(ctx, client.PingOptions{})
	if err != nil {
		// gomutants:disable-next-line RETURN_ZERO reason="builderClassic is builderKind's zero value, so the RETURN_ZERO replacement is the identical constant"
		return builderClassic, err
	}
	if ping.BuilderVersion == build.BuilderBuildKit {
		return builderBuildKit, nil
	}
	// gomutants:disable-next-line RETURN_ZERO reason="builderClassic is builderKind's zero value, so the RETURN_ZERO replacement is the identical constant"
	return builderClassic, nil
}

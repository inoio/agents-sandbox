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
// Builder-Version header: BuilderBuildKit ("2") selects the BuildKit path, and
// anything else — including BuilderV1 and an absent header, which Docker Engine
// and podman both report — the classic path. A ping failure surfaces unchanged:
// a build on an unreachable engine must not silently assume a builder.
func selectBuilder(ctx context.Context, dclient docker.Client) (builderKind, error) {
	ping, err := dclient.Ping(ctx, client.PingOptions{})
	if err != nil {
		return builderClassic, err
	}
	if ping.BuilderVersion == build.BuilderBuildKit {
		return builderBuildKit, nil
	}
	return builderClassic, nil
}

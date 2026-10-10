package builder

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// HostPlatform is the linux platform matching the host architecture, e.g. the
// architecture of the agent binaries the runner image must carry.
func HostPlatform() string {
	if runtime.GOARCH == "arm64" {
		return "linux/arm64"
	}
	return "linux/amd64"
}

// normalizePlatform converts a platform specifier such as "linux/amd64" into an
// OCI platform for the gateway resolver, returning nil for empty/unparseable
// input so the resolver falls back to its own default.
func normalizePlatform(platform string) *ocispecs.Platform {
	if platform == "" {
		return nil
	}
	parsed, err := platforms.Parse(platform)
	if err != nil {
		return nil
	}
	normalized := platforms.Normalize(parsed)
	return &normalized
}

// resolveBaseDigest resolves a base image reference to its content digest via
// BuildKit, producing "<baseRef>@<digest>". It is a variable so tests can stub
// resolution without a live builder.
var resolveBaseDigest = realResolveBaseDigest

func realResolveBaseDigest(
	ctx context.Context,
	bc buildkitClient,
	baseRef string,
	platform string,
) (string, error) {
	_, digest, _, err := bc.ResolveImageConfig(
		ctx, baseRef, sourceresolver.Opt{Platform: normalizePlatform(platform)},
	)
	if err != nil {
		return "", err
	}
	return baseRef + "@" + digest.String(), nil
}

// forwardProgress reports non-empty log lines and cache hits from a build status
// to the caller's progress callback. Cached vertices are surfaced so callers can
// observe BuildKit layer-cache reuse (the cached RUN steps emit no logs).
func forwardProgress(status *client.SolveStatus, progress func(string)) {
	if progress == nil {
		return
	}
	for _, log := range status.Logs {
		if line := strings.TrimSpace(string(log.Data)); line != "" {
			progress(line)
		}
	}
	for _, vertex := range status.Vertexes {
		if vertex.Cached {
			progress(fmt.Sprintf("%s CACHED", vertex.Name))
		}
	}
}

// nopWriteCloser lets BuildKit's session stream the export and call Close on
// the writer without closing the underlying archive file: the session closes
// every writer it is handed, and a second close of the *os.File would fail the
// build. Build owns the single real Close after Solve returns.
type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

package image

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

// testBuildRequest assembles a docker-off buildRequest for the build-level tests
// that call buildImage/buildDockerImage directly, mapping the loose values those
// tests vary onto the request fields. Tests exercising docker enablement go
// through EnsureImageWithClient or set the request's docker field directly.
func testBuildRequest(
	rendered []byte,
	agentVersion, baseDigest, dockerfileID string,
	identity imageIdentity,
) buildRequest {
	return buildRequest{
		plan: buildPlan{
			rendered:     rendered,
			agentVersion: agentVersion,
			dockerfileID: dockerfileID,
			identity:     identity,
		},
		baseDigest: baseDigest,
	}
}

// TestUserBuildArgsKeepsResolvedBaseOutOfIdentityBase pins the naming split the
// build request introduces: BASE_IMAGE carries the resolved base digest, while
// the identity's cached base (a different concept) is never used as the label.
func TestUserBuildArgsKeepsResolvedBaseOutOfIdentityBase(t *testing.T) {
	req := buildRequest{
		plan:       buildPlan{identity: imageIdentity{BaseImage: "debian:trixie-slim@sha256:cached"}},
		baseDigest: "debian:trixie-slim@sha256:resolved",
	}
	args := userBuildArgs(1000, 1000, agentOpencode(t).ImageSpec(), req)
	if got := args["BASE_IMAGE"]; got == nil || *got != "debian:trixie-slim@sha256:resolved" {
		t.Errorf("BASE_IMAGE = %v, want the resolved base digest", got)
	}
}

// TestUserBuildArgsDockerVersionFollowsRequestSetting pins that the
// DOCKER_VERSION arg is driven by the request's resolved docker setting alone.
func TestUserBuildArgsDockerVersionFollowsRequestSetting(t *testing.T) {
	spec := agentOpencode(t).ImageSpec()
	off := userBuildArgs(1000, 1000, spec, buildRequest{docker: options.DockerSetting{}})
	if off["DOCKER_VERSION"] != nil {
		t.Errorf("DOCKER_VERSION = %v, want absent when the request disables docker", off["DOCKER_VERSION"])
	}
	on := userBuildArgs(1000, 1000, spec, buildRequest{docker: options.DockerSetting{Enabled: true, Explicit: true}})
	if v := on["DOCKER_VERSION"]; v == nil || *v != dockerVersion {
		t.Errorf("DOCKER_VERSION = %v, want %q", v, dockerVersion)
	}
}

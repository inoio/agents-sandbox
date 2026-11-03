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

// TestBuildRequestKeepsRawAndEffectiveDockerApart pins the deliberate divergence
// between the request's raw docker setting and the identity's effective docker
// value. When the project Dockerfile's final-stage marker implies docker with no
// explicit config, DOCKER_VERSION is absent (the raw setting is off) while the
// identity's docker field is "true" (the effective value is on). A future
// "simplification" that derives one from the other would change the build args,
// so this locks the behavior.
func TestBuildRequestKeepsRawAndEffectiveDockerApart(t *testing.T) {
	project := []byte("FROM debian:trixie-slim\n# agents-sandbox:docker\nRUN echo hi\n")
	plan, err := renderBuildPlan(agentOpencode(t), parseProjectDockerfile(project), dockerOff, "1.2.3", "1.2.3")
	if err != nil {
		t.Fatalf("renderBuildPlan: %v", err)
	}
	if plan.identity.Docker != "true" {
		t.Fatalf("identity.Docker = %q, want the effective on value", plan.identity.Docker)
	}

	args := userBuildArgs(1000, 1000, agentOpencode(t).ImageSpec(), buildRequest{plan: plan, docker: dockerOff})

	if v := args["DOCKER_VERSION"]; v != nil {
		t.Errorf("DOCKER_VERSION = %q, want absent for the raw off setting", *v)
	}
	if got := args[dockerArg]; got == nil || *got != "true" {
		t.Errorf("IDENTITY_DOCKER = %v, want %q", got, "true")
	}
}

package image

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestComputeDockerfileIDFoldsMergeScript asserts the identity is stable for
// identical inputs and changes when the embedded profile.d merge script changes,
// so editing the shipped script invalidates existing runner images.
func TestComputeDockerfileIDFoldsMergeScript(t *testing.T) {
	rendered := []byte("FROM scratch\n")
	original := embeddedPathMergeScript
	t.Cleanup(func() { embeddedPathMergeScript = original })

	before := computeDockerfileID(rendered, "1.2.3")
	if again := computeDockerfileID(rendered, "1.2.3"); again != before {
		t.Fatalf("computeDockerfileID not stable for identical inputs: %q vs %q", before, again)
	}

	embeddedPathMergeScript = append(append([]byte(nil), original...), []byte("\n# changed\n")...)
	if after := computeDockerfileID(rendered, "1.2.3"); after == before {
		t.Error("computeDockerfileID did not change after the merge script changed")
	}
}

// msbInspector installs an msb mock whose ImageInspect reports present images
// from labels (absent when labels is nil) and reads digest/env from the config.
func msbInspector(t *testing.T, inspectFn func(context.Context, string) (*msbSdk.ImageConfig, error)) {
	t.Helper()
	msb.WithMsbMock(t, &msb.MockMsbClient{ImageInspectFn: inspectFn})
}

// TestEnsureImageSkipsBuildWhenDockerfileIDMatches verifies the build is skipped
// when the existing runner image already carries a matching dockerfile-id label.
func TestEnsureImageSkipsBuildWhenDockerfileIDMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(renderBytes(t, a, nil, false), "1.2.3"),
	}
	msbInspector(t, func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return msbImageConfig("sha256:existing", labels, nil), nil
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/should-not-use"}, nil)

	info, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Error("expected the build to be skipped when the dockerfile-id label matches")
	}
	if info.Digest != "sha256:existing" {
		t.Errorf("Digest = %q, want the existing msb digest", info.Digest)
	}
}

// TestEnsureImageDryRunSkipsBuilder verifies that a dry run reports the build
// plan (tag) without booting the builder VM: when a build would be needed, the
// builder is not called and no archive is produced.
func TestEnsureImageDryRunSkipsBuilder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msbInspector(t, func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return nil, errors.New("image not found")
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	info, err := EnsureImage(context.Background(), a, "proj", BuildOptions{DryRun: true}, &termio.Mock{})
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Errorf("builder calls = %d, want 0 for a dry run", len(mock.Calls))
	}
	if info.Tag == "" {
		t.Error("expected the computed runner tag in a dry-run ImageInfo")
	}
	if info.ArchivePath != "" || info.Digest != "" {
		t.Errorf("dry-run ImageInfo = %+v, want empty Digest and ArchivePath", info)
	}
}

// TestEnsureImageReportsRenderError verifies a project Dockerfile that declares
// a reserved stage alias surfaces the render error before any build runs.
func TestEnsureImageReportsRenderError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	project := []byte("FROM debian:trixie-slim AS agents-sandbox-base\n")

	_, err := EnsureImageWithClient(context.Background(), a, project, "proj", BuildOptions{}, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "render Dockerfile") {
		t.Fatalf("EnsureImageWithClient error = %v, want a render Dockerfile error", err)
	}
}

// TestEnsureImageBuildsWhenDockerfileIDMismatches verifies the build runs when
// the existing runner image's dockerfile-id label differs from the current one.
func TestEnsureImageBuildsWhenDockerfileIDMismatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msbInspector(t, func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return msbImageConfig("sha256:existing", map[string]string{dockerfileIDLabelKey: "stale"}, nil), nil
	})
	mock := fakeBuilder(t, builder.BuildResult{
		ArchivePath: "/tmp/a", Digest: "sha256:new", Env: []string{"PATH=/usr/bin"},
	}, nil)

	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if len(mock.Calls) != 1 {
		t.Errorf("builder calls = %d, want 1 when the dockerfile-id label mismatches", len(mock.Calls))
	}
}

// TestEnsureImageBuildArgsIncludeAgentVersion asserts the pinned agent version
// arg is passed to the builder and BASE_IMAGE is left to the builder.
func TestEnsureImageBuildArgsIncludeAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msbInspector(t, func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return nil, errors.New("image not found")
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	args := mock.Calls[0].BuildArgs
	if args["OPENCODE_VERSION"] != "1.2.3" {
		t.Errorf("OPENCODE_VERSION = %q, want 1.2.3", args["OPENCODE_VERSION"])
	}
	if _, ok := args["BASE_IMAGE"]; ok {
		t.Error("BASE_IMAGE must not be set by the image package")
	}
}

// TestEnsureImageResolvesAnUnpinnedAgentVersion verifies that an empty version
// is resolved to a real release before it is passed to the builder.
func TestEnsureImageResolvesAnUnpinnedAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	var requested string
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		requested = req
		return "1.2.3", nil
	})
	msbInspector(t, func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return nil, errors.New("image not found")
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if requested != "" {
		t.Errorf("resolver requested = %q, want empty version", requested)
	}
	if got := mock.Calls[0].BuildArgs["OPENCODE_VERSION"]; got != "1.2.3" {
		t.Errorf("OPENCODE_VERSION build arg = %q, want 1.2.3", got)
	}
}

// TestEnsureImageReusesUnknownUserAgentWithoutResolvingVersion verifies that
// an existing user-provided image can be reused without contacting the release
// endpoint when its version was not recorded.
func TestEnsureImageReusesUnknownUserAgentWithoutResolvingVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	// The deferred path renders before the version is known, so the identity
	// uses the version-less render.
	rendered := renderBytesVersion(t, a, "", nil, false)
	wantID := computeDockerfileID(rendered, userProvidedImageIdentity)
	resolverCalled := false
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		resolverCalled = true
		return "1.2.3", nil
	})
	msbInspector(t, func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return msbImageConfig("sha256:existing", map[string]string{dockerfileIDLabelKey: wantID}, nil), nil
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a"}, nil)

	if _, err := EnsureImage(
		context.Background(), a, "proj", BuildOptions{UserProvided: true}, &termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if resolverCalled {
		t.Error("user-provided image reuse must not resolve an agent version")
	}
	if len(mock.Calls) != 0 {
		t.Error("user-provided image reuse must not rebuild a matching image")
	}
}

// TestEnsureImageResolvesUnknownUserAgentOnlyForARebuild verifies that a stale
// user-provided image gets a real installer version and records that resolved
// version in the image identity for subsequent reuse.
func TestEnsureImageResolvesUnknownUserAgentOnlyForARebuild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		if requested != "" {
			t.Errorf("resolver requested = %q, want empty version", requested)
		}
		return "1.2.3", nil
	})
	msbInspector(t, func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return msbImageConfig("sha256:existing", map[string]string{dockerfileIDLabelKey: "stale"}, nil), nil
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	if _, err := EnsureImage(
		context.Background(), a, "proj", BuildOptions{UserProvided: true}, &termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	args := mock.Calls[0].BuildArgs
	if args["OPENCODE_VERSION"] != "1.2.3" {
		t.Errorf("OPENCODE_VERSION build arg = %q, want 1.2.3", args["OPENCODE_VERSION"])
	}
	wantID := computeDockerfileID(renderBytes(t, a, nil, false), "1.2.3")
	if args["DOCKERFILE_ID"] != wantID {
		t.Errorf("DOCKERFILE_ID = %q, want resolved-version identity %q", args["DOCKERFILE_ID"], wantID)
	}
}

// TestEnsureImageDockerAddsDockerVersionArg verifies the docker build arg is only
// passed when dind is enabled.
func TestEnsureImageDockerAddsDockerVersionArg(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msbInspector(t, func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return nil, errors.New("image not found")
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{Docker: true}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if got := mock.Calls[0].BuildArgs["DOCKER_VERSION"]; got != "29.7.2" {
		t.Errorf("DOCKER_VERSION build arg = %q, want 29.7.2", got)
	}
}

func TestBaseImageRef(t *testing.T) {
	a := agentOpencode(t)
	if got := baseImageRef(renderBytes(t, a, nil, false)); got != "debian:trixie-slim" {
		t.Errorf("default baseImageRef = %q", got)
	}
	custom := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, custom, false)); got != "ubuntu:24.04" {
		t.Errorf("custom baseImageRef = %q", got)
	}
}

package image

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestEnsureImageWithClientSetsContentRef pins that the built image's content id
// keys the microsandbox reference, while the Docker tag stays the slug/agent name.
func TestEnsureImageWithClientSetsContentRef(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     testDockerImageID,
				Config: dockerConfigWith(testAgentVersion, nil),
			}, nil
		},
	})

	info, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"myproj-1abc1234defg5",
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if want := "myproj-1abc1234defg5:opencode.9f2c1a7b4d6e0f31"; info.Ref != want {
		t.Errorf("info.Ref = %q, want the content ref %q", info.Ref, want)
	}
	if want := naming.DockerImageRef("myproj-1abc1234defg5", "opencode"); info.DockerTag != want {
		t.Errorf("info.DockerTag = %q, want the unchanged Docker tag %q", info.DockerTag, want)
	}
	if want := (naming.ImageIdentity{
		Slug: "myproj-1abc1234defg5", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31",
	}); info.Identity != want {
		t.Errorf("info.Identity = %+v, want the structured identity %+v", info.Identity, want)
	}
}

// TestEnsureImageWithClientFallsBackToNameRefWhenContentIDEmpty pins the
// no-content-id fallback: an image whose inspect reports no ID keeps the
// prefix-free name reference rather than emitting a dangling content suffix.
func TestEnsureImageWithClientFallsBackToNameRefWhenContentIDEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{Config: dockerConfigWith("1.2.3", nil)}, nil
		},
	})

	info, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"myproj-1abc1234defg5",
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if want := naming.RunnerImageRef("myproj-1abc1234defg5", "opencode"); info.Ref != want {
		t.Errorf("info.Ref = %q, want the name ref %q", info.Ref, want)
	}
	if want := (naming.ImageIdentity{Slug: "myproj-1abc1234defg5", Agent: "opencode"}); info.Identity != want {
		t.Errorf("info.Identity = %+v, want the identity with no content id %+v", info.Identity, want)
	}
}

func TestImageContentIDTrimsAndTruncates(t *testing.T) {
	if got := imageContentID(testDockerImageID); got != "9f2c1a7b4d6e0f31" {
		t.Errorf("imageContentID(%q) = %q, want the sha256-prefix-free 16-char prefix", testDockerImageID, got)
	}
	if got := imageContentID(""); got != "" {
		t.Errorf("imageContentID(\"\") = %q, want empty", got)
	}
	if got := imageContentID(
		"sha256:9F2C1A7B4D6E0F3199999999999999999999999999999999999999999999999999",
	); got != "9f2c1a7b4d6e0f31" {
		t.Errorf("imageContentID(uppercase id) = %q, want the lowercased 16-char prefix", got)
	}
	// A 17-character id pins the exclusive bound: it must still truncate to 16.
	if got := imageContentID("sha256:0123456789abcdefg"); got != "0123456789abcdef" {
		t.Errorf("imageContentID(17-char id) = %q, want the 16-char prefix", got)
	}
}

func TestRenderBuildPlanRecordsHostUser(t *testing.T) {
	plan, err := renderBuildPlan(agentOpencode(t), parseProjectDockerfile(nil), dockerOff, "1.2.3", "1.2.3")
	if err != nil {
		t.Fatalf("renderBuildPlan: %v", err)
	}
	if want := hostUserIdentity(os.Getuid(), os.Getgid()); plan.identity.User != want {
		t.Errorf("plan identity User = %q, want %q", plan.identity.User, want)
	}
}

// TestRenderBuildPlanIdentityHashesProjectDockerfile pins that the parsed value
// carries the project bytes into the image identity: the identity's Dockerfile
// hash must be the hash of the project Dockerfile, not the absent-input sentinel.
func TestRenderBuildPlanIdentityHashesProjectDockerfile(t *testing.T) {
	project := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	plan, err := renderBuildPlan(
		agentOpencode(t), parseProjectDockerfile(project), dockerOff, testAgentVersion, testAgentVersion,
	)
	if err != nil {
		t.Fatalf("renderBuildPlan: %v", err)
	}
	if want := hashOrNone(project); plan.identity.ProjectDockerfileHash != want {
		t.Errorf("identity ProjectDockerfileHash = %q, want %q", plan.identity.ProjectDockerfileHash, want)
	}
}

func TestPlanRunnerImageRebuildsWhenLocalBaseAdvanced(t *testing.T) {
	runnerAndBaseMock(t, seededRunnerLabels(t, "debian:trixie-slim@sha256:old"), "sha256:new")

	_, _, needsBuild, reasons, err := planRunnerImage(
		context.Background(),
		docker.Get(),
		agentOpencode(t),
		parseProjectDockerfile(nil),
		"proj",
		"1.2.3",
		"1.2.3",
		BuildOptions{Docker: dockerOff},
	)
	if err != nil {
		t.Fatalf("planRunnerImage: %v", err)
	}
	if !needsBuild || strings.Join(reasons, "|") != "base image updated" {
		t.Errorf("needsBuild = %v, reasons = %v, want a rebuild for the updated base", needsBuild, reasons)
	}
}

func TestPlanRunnerImageReusesImageWhenBaseUnchangedOrAbsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		baseID string
	}{
		{"base unchanged", "sha256:same"},
		{"base absent locally", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runnerAndBaseMock(t, seededRunnerLabels(t, "debian:trixie-slim@sha256:same"), tc.baseID)

			_, _, needsBuild, reasons, err := planRunnerImage(
				context.Background(),
				docker.Get(),
				agentOpencode(t),
				parseProjectDockerfile(nil),
				"proj",
				"1.2.3",
				"1.2.3",
				BuildOptions{Docker: dockerOff},
			)
			if err != nil {
				t.Fatalf("planRunnerImage: %v", err)
			}
			if needsBuild {
				t.Errorf("expected the image to be reused, got reasons %v", reasons)
			}
		})
	}
}

// TestPlanRunnerImageNoRebuildReusesChangedImage pins the --no-rebuild reuse: a
// present image whose inputs changed is reused, and the skipped reasons are
// still reported so the caller can name them.
func TestPlanRunnerImageNoRebuildReusesChangedImage(t *testing.T) {
	runnerAndBaseMock(t, seededRunnerLabels(t, "debian:trixie-slim@sha256:old"), "sha256:new")

	_, _, needsBuild, reasons, err := planRunnerImage(
		context.Background(),
		docker.Get(),
		agentOpencode(t),
		parseProjectDockerfile(nil),
		"proj",
		"1.2.3",
		"1.2.3",
		BuildOptions{Docker: dockerOff, NoRebuild: true},
	)
	if err != nil {
		t.Fatalf("planRunnerImage: %v", err)
	}
	if needsBuild {
		t.Error("needsBuild = true, want the changed image reused under --no-rebuild")
	}
	if !slices.Equal(reasons, []string{"base image updated"}) {
		t.Errorf("reasons = %v, want the skipped base-image reason", reasons)
	}
}

// TestPlanRunnerImageNoRebuildStillBuildsMissingImage pins that --no-rebuild
// only skips a rebuild: without an image there is nothing to reuse.
func TestPlanRunnerImageNoRebuildStillBuildsMissingImage(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("No such image")
		},
	})

	_, _, needsBuild, reasons, err := planRunnerImage(
		context.Background(),
		docker.Get(),
		agentOpencode(t),
		parseProjectDockerfile(nil),
		"proj",
		"1.2.3",
		"1.2.3",
		BuildOptions{Docker: dockerOff, NoRebuild: true},
	)
	if err != nil {
		t.Fatalf("planRunnerImage: %v", err)
	}
	if !needsBuild {
		t.Fatal("needsBuild = false, want a build when the runner image is absent")
	}
	if !slices.Equal(reasons, []string{"runner image not present locally"}) {
		t.Errorf("reasons = %v, want the not-present reason", reasons)
	}
}

func TestLogBuildDecisionNamesSkippedRebuildReasons(t *testing.T) {
	ui := &termio.Mock{}

	logBuildDecision(ui, "runner:tag", false, []string{"project Dockerfile changed", "agent version changed"},
		"opencode", "1.2.3", "")

	want := "reusing runner image runner:tag despite changes (--no-rebuild): " +
		"project Dockerfile changed; agent version changed"
	if !slices.Contains(ui.InfoCalls, want) {
		t.Errorf("InfoCalls = %v, want %q", ui.InfoCalls, want)
	}
}

func TestLogBuildDecisionReuseWithoutChangesStaysVerbose(t *testing.T) {
	ui := &termio.Mock{}

	logBuildDecision(ui, "runner:tag", false, nil, "opencode", "1.2.3", "")

	if len(ui.InfoCalls) != 0 {
		t.Errorf("InfoCalls = %v, want none for an unchanged image", ui.InfoCalls)
	}
	if !slices.ContainsFunc(ui.VerboseCalls, func(call string) bool {
		return strings.Contains(call, "content identity unchanged")
	}) {
		t.Errorf("VerboseCalls = %v, want the unchanged-identity line", ui.VerboseCalls)
	}
}

func TestLogBuildDecisionNamesSingleSkippedRebuildReason(t *testing.T) {
	ui := &termio.Mock{}

	logBuildDecision(ui, "runner:tag", false, []string{"base image updated"}, "opencode", "1.2.3", "")

	want := "reusing runner image runner:tag despite changes (--no-rebuild): base image updated"
	if !slices.Contains(ui.InfoCalls, want) {
		t.Errorf("InfoCalls = %v, want %q", ui.InfoCalls, want)
	}
}

func TestEnsureImageReportsBakedAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	rendered, err := RenderDockerfile(a, "1.2.3", nil, dockerOff)
	if err != nil {
		t.Fatalf("render Dockerfile: %v", err)
	}
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(rendered, "1.2.3"),
		agentVersionLabelKey: "1.2.3",
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
	})

	info, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if info.AgentVersion != "1.2.3" {
		t.Errorf("AgentVersion = %q, want 1.2.3", info.AgentVersion)
	}
}

func TestEnsureImageReportsEmptyForSentinelLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	rendered, err := RenderDockerfile(a, "1.2.3", nil, dockerOff)
	if err != nil {
		t.Fatalf("render Dockerfile: %v", err)
	}
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(rendered, "1.2.3"),
		agentVersionLabelKey: userProvidedImageIdentity,
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
	})

	info, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if info.AgentVersion != "" {
		t.Errorf("AgentVersion = %q, want empty for a user-provided label", info.AgentVersion)
	}
}

// TestEnsureImageSkipsBuildWhenDockerfileIDMatches verifies the build is skipped
// when the existing runner image already carries a matching dockerfile-id label.
func TestEnsureImageSkipsBuildWhenDockerfileIDMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			id := "sha256:existing"
			labels := map[string]string{
				dockerfileIDLabelKey: computeDockerfileID(renderBytes(t, a, nil, dockerOff), "1.2.3"),
			}
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     id,
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			built = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	info, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if built {
		t.Error("expected the docker build to be skipped when the dockerfile-id label matches")
	}
	if info.Digest != "sha256:existing" {
		t.Errorf("Digest = %q, want the existing image ID", info.Digest)
	}
}

// TestEnsureImageReportsRenderError verifies a project Dockerfile that declares
// a reserved stage alias surfaces the render error before any build runs.
func TestEnsureImageReportsRenderError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	project := []byte("FROM debian:trixie-slim AS agents-sandbox-base\n")

	_, err := EnsureImageWithClient(
		context.Background(),
		docker.Get(),
		a,
		project,
		"proj",
		BuildOptions{},
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "render Dockerfile") {
		t.Fatalf("EnsureImageWithClient error = %v, want a render Dockerfile error", err)
	}
}

// TestEnsureImageBuildsWhenDockerfileIDMismatches verifies the build runs when
// the existing runner image's dockerfile-id label differs from the current one.
func TestEnsureImageBuildsWhenDockerfileIDMismatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: "stale"},
				},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			built = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !built {
		t.Error("expected the docker build to run when the dockerfile-id label mismatches")
	}
}

// TestEnsureImageBuildArgsIncludeBaseAndAgentVersion asserts the BASE_IMAGE
// provenance build arg and the pinned agent version arg.
func TestEnsureImageBuildArgsIncludeBaseAndAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	var gotArgs map[string]*string
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	_, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	for key, want := range map[string]string{
		"BASE_IMAGE":       "debian:trixie-slim@sha256:base",
		"OPENCODE_VERSION": "1.2.3",
	} {
		if gotArgs == nil || gotArgs[key] == nil || *gotArgs[key] != want {
			t.Errorf("build arg %s = %v, want %q", key, gotArgs, want)
		}
	}
}

// TestEnsureImageResolvesAnUnpinnedAgentVersion verifies that an empty version
// is resolved to a real release before it is passed to the Docker build.
func TestEnsureImageResolvesAnUnpinnedAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	var requested string
	var gotArgs map[string]*string
	withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		requested = req
		return "1.2.3", nil
	})
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if requested != "" {
		t.Errorf("resolver requested = %q, want empty version", requested)
	}
	if gotArgs == nil || gotArgs["OPENCODE_VERSION"] == nil {
		t.Fatalf("OPENCODE_VERSION build arg missing: %v", gotArgs)
	}
	if got := *gotArgs["OPENCODE_VERSION"]; got != "1.2.3" {
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
	rendered := renderBytesVersion(t, a, "", nil, dockerOff)
	wantID := computeDockerfileID(rendered, userProvidedImageIdentity)
	buildCalled := false
	resolverCalled := false
	withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		resolverCalled = true
		return "1.2.3", nil
	})
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: wantID},
				},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			buildCalled = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(context.Background(), docker.Get(), configpaths.Get(), a,
		"proj",
		nil,
		BuildOptions{UserProvided: true},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if resolverCalled {
		t.Error("user-provided image reuse must not resolve an agent version")
	}
	if buildCalled {
		t.Error("user-provided image reuse must not rebuild a matching image")
	}
}

// TestEnsureImageResolvesUnknownUserAgentOnlyForARebuild verifies that a stale
// user-provided image gets a real installer version and records that resolved
// version in the image identity for subsequent reuse.
func TestEnsureImageResolvesUnknownUserAgentOnlyForARebuild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	var gotArgs map[string]*string
	var gotDockerfileID string
	withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		if requested != "" {
			t.Errorf("resolver requested = %q, want empty version", requested)
		}
		return "1.2.3", nil
	})
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: "stale"},
				},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			if value := opts.BuildArgs["DOCKERFILE_ID"]; value != nil {
				gotDockerfileID = *value
			}
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(context.Background(), docker.Get(), configpaths.Get(), a,
		"proj",
		nil,
		BuildOptions{UserProvided: true},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if gotArgs == nil || gotArgs["OPENCODE_VERSION"] == nil || *gotArgs["OPENCODE_VERSION"] != "1.2.3" {
		t.Errorf("OPENCODE_VERSION build arg = %v, want 1.2.3", gotArgs)
	}
	wantID := computeDockerfileID(renderBytes(t, a, nil, dockerOff), "1.2.3")
	if gotDockerfileID != wantID {
		t.Errorf("DOCKERFILE_ID = %q, want resolved-version identity %q", gotDockerfileID, wantID)
	}
}

// TestEnsureImageDockerAddsDockerVersionArg verifies the docker build arg is only
// passed when dind is enabled.
func TestEnsureImageDockerAddsDockerVersionArg(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	var gotArgs map[string]*string
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(context.Background(), docker.Get(), configpaths.Get(), a,
		"proj",
		nil,
		BuildOptions{Docker: dockerOn},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if gotArgs == nil || gotArgs["DOCKER_VERSION"] == nil || *gotArgs["DOCKER_VERSION"] != "29.7.2" {
		t.Errorf("DOCKER_VERSION build arg = %v, want 29.7.2", gotArgs)
	}
}

func TestEnsureImageVerboseNamesChangedInputs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "2.0.0")
	a := agentOpencode(t)
	oldLabels := identityLabels(computeImageIdentity(nil, "1.0.0", false))
	oldLabels[dockerfileIDLabelKey] = "stale"
	built := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, ref string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: oldLabels},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			built = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	ui := &termio.Mock{}
	if _, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{},
		ui,
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !built {
		t.Fatal("expected a build when the identity changed")
	}
	if !verboseContains(ui, "rebuilding runner image") || !verboseContains(ui, "agent version changed") {
		t.Errorf("verbose calls = %v, want the rebuild reason", ui.VerboseCalls)
	}
}

func TestEnsureImageVerboseReportsReuse(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	rendered, err := RenderDockerfile(a, testAgentVersion, nil, dockerOff)
	if err != nil {
		t.Fatalf("render Dockerfile: %v", err)
	}
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(rendered, "1.2.3"),
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, ref string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
	})
	ui := &termio.Mock{}
	if _, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{},
		ui,
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !verboseContains(ui, "reusing runner image") {
		t.Errorf("verbose calls = %v, want a reuse line", ui.VerboseCalls)
	}
}

func TestEnsureImageVerboseReportsForcedRebuild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	ui := &termio.Mock{}
	if _, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"proj",
		nil,
		BuildOptions{Force: true},
		ui,
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !verboseContains(ui, "forced by --rebuild") {
		t.Errorf("verbose calls = %v, want a forced-rebuild reason", ui.VerboseCalls)
	}
}

// TestEnsureImageFailsWhenAStepFails pins that each step's failure is reported:
// resolving the agent version up front, resolving it late for an unknown
// user-provided version that needs a rebuild, the build itself, and inspecting
// the built image.
func TestEnsureImageFailsWhenAStepFails(t *testing.T) {
	readEnvErr := errors.New("read env boom")
	failingResolver := func(t *testing.T) {
		t.Helper()
		withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, requested string) (string, error) {
			if requested != "" {
				t.Errorf("resolver requested = %q, want empty version", requested)
			}
			return "", errors.New("resolve boom")
		})
	}
	builtThenInspect := func(inspectBuilt func(inspectAfterBuild int) (client.ImageInspectResult, error)) func(*testing.T) {
		return func(t *testing.T) {
			t.Helper()
			withMockAgentVersion(t, "1.2.3")
			built, inspectsAfterBuild := false, 0
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					if !built {
						return client.ImageInspectResult{ID: "sha256:base"}, nil
					}
					inspectsAfterBuild++
					return inspectBuilt(inspectsAfterBuild)
				},
				ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
					built = true
					return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
				},
			})
		}
	}
	for _, tc := range []struct {
		name      string
		setup     func(*testing.T)
		opts      BuildOptions
		wantErr   string
		wantCause error
	}{
		{
			name:    "version resolution fails",
			setup:   failingResolver,
			wantErr: "resolve boom",
		},
		{
			name: "deferred version resolution fails",
			setup: func(t *testing.T) {
				t.Helper()
				failingResolver(t)
				docker.WithDockerMock(t, &docker.MockDockerClient{
					ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
						return client.ImageInspectResult{
							ID: "sha256:existing",
							Config: &dockerspec.DockerOCIImageConfig{
								Labels: map[string]string{dockerfileIDLabelKey: "stale"},
							},
						}, nil
					},
				})
			},
			opts:    BuildOptions{UserProvided: true},
			wantErr: "resolve boom",
		},
		{
			name: "build fails",
			setup: func(t *testing.T) {
				t.Helper()
				withMockAgentVersion(t, "1.2.3")
				docker.WithDockerMock(t, &docker.MockDockerClient{
					ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
						return client.ImageInspectResult{ID: "sha256:base"}, nil
					},
					ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
						return client.ImageBuildResult{}, errors.New("build boom")
					},
				})
			},
			opts:    BuildOptions{Force: true},
			wantErr: "build boom",
		},
		{
			name: "built image cannot be inspected",
			setup: builtThenInspect(func(int) (client.ImageInspectResult, error) {
				return client.ImageInspectResult{}, errors.New("inspect boom")
			}),
			opts:    BuildOptions{Force: true},
			wantErr: "cannot inspect built image",
		},
		{
			name: "built image info cannot be read",
			setup: builtThenInspect(func(inspectAfterBuild int) (client.ImageInspectResult, error) {
				if inspectAfterBuild == 1 {
					return client.ImageInspectResult{ID: "sha256:built", Config: dockerConfigWith("", nil)}, nil
				}
				return client.ImageInspectResult{}, readEnvErr
			}),
			opts:      BuildOptions{Force: true},
			wantErr:   "inspect built image",
			wantCause: readEnvErr,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			tc.setup(t)
			_, err := EnsureImageWithClient(context.Background(), docker.Get(), agentOpencode(t),
				[]byte("FROM debian:trixie-slim\n"), "proj", tc.opts, &termio.Mock{},
			)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("EnsureImageWithClient error = %v, want it to contain %q", err, tc.wantErr)
			}
			if tc.wantCause != nil && !errors.Is(err, tc.wantCause) {
				t.Errorf("EnsureImageWithClient error = %v, want it to wrap %v", err, tc.wantCause)
			}
		})
	}
}

// TestEnsureImageWarnsOnLegacyDockerfileForms verifies the warnings are emitted
// through the UI when a project Dockerfile using a legacy dind spelling is
// ensured for building.
func TestEnsureImageWarnsOnLegacyDockerfileForms(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: "stale"},
				},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})

	project := []byte("FROM agents-sandbox/runner-base-dind\n# agents-sandbox:dind\nRUN echo hi\n")
	ui := &termio.Mock{}
	if _, err := EnsureImageWithClient(
		context.Background(),
		docker.Get(),
		a,
		project,
		"proj",
		BuildOptions{},
		ui,
	); err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}

	joined := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{"runner-base-dind", "# agents-sandbox:dind"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a deprecation warning mentioning %q, got %v", want, ui.WarnCalls)
		}
	}
}

// TestEnsureImageWarnsOnEveryEnsure pins the documented behavior that the
// project-Dockerfile deprecations warn on every ensure, including a cached
// reuse: a Dockerfile carrying both legacy forms yields two warnings per call.
func TestEnsureImageWarnsOnEveryEnsure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: "stale"},
				},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})

	project := []byte("FROM agents-sandbox/runner-base-dind\n# agents-sandbox:dind\nRUN echo hi\n")
	ui := &termio.Mock{}
	for range 2 {
		if _, err := EnsureImageWithClient(
			context.Background(),
			docker.Get(),
			a,
			project,
			"proj",
			BuildOptions{},
			ui,
		); err != nil {
			t.Fatalf("EnsureImageWithClient: %v", err)
		}
	}

	count := 0
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, "runner-base-dind") || strings.Contains(warning, "# agents-sandbox:dind") {
			count++
		}
	}
	if count != 4 {
		t.Errorf(
			"expected 2 deprecation warnings per ensure across 2 ensures (4 total), got %d: %v",
			count,
			ui.WarnCalls,
		)
	}
}

func TestEnsureImageDoesNotCreateDigestAliasTag(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	var tagged []string
	m := &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:abc123"}, nil
		},
		ImageTagFn: func(_ context.Context, opts client.ImageTagOptions) (client.ImageTagResult, error) {
			tagged = append(tagged, opts.Target)
			return client.ImageTagResult{}, nil
		},
	}
	docker.WithDockerMock(t, m)

	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		dockerfile,
		"test-project",
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tagged) != 0 {
		t.Errorf("expected no digest alias tags, got: %v", tagged)
	}
}

func TestEnsureImageDoesNotLoadIntoMSB(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     "sha256:abc123",
				Config: dockerConfigWith("1.0.0", []string{"PATH=/usr/bin"}),
			}, nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error {
			return errors.New("image not in cache")
		},
	}
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest\nRUN echo hi\n")
	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		dockerfile,
		"test-project",
		BuildOptions{Force: false},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("EnsureImage must not load into microsandbox; got %d loads", len(msbClient.LoadedImages))
	}
}

// TestEnsureImageResolvesBaseDigestByRenderedRef pins that the base image is
// inspected and pulled by exactly the reference the rendered Dockerfile builds
// from, so the lookup and the build agree on one image: the tool-owned base is
// qualified (its rendered FROM is), while a user's own base keeps the name the
// engine will resolve — qualifying that would send it to Docker Hub even when
// the engine maps the name to a mirror or a private registry. The recorded
// BASE_IMAGE label keeps the familiar spelling either way.
func TestEnsureImageResolvesBaseDigestByRenderedRef(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	orig := defaultAgentVersionResolver
	defaultAgentVersionResolver = func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		return requested, nil
	}
	t.Cleanup(func() { defaultAgentVersionResolver = orig })

	for _, tc := range []struct {
		name             string
		project          []byte
		wantLookup       string
		wantBaseImageArg string
	}{
		{
			name:             "managed base is looked up qualified",
			project:          nil,
			wantLookup:       distroBaseRef,
			wantBaseImageArg: "debian:trixie-slim@sha256:base",
		},
		{
			name:             "a user's base keeps the name the engine resolves",
			project:          []byte("FROM company/base:latest\nRUN echo hi\n"),
			wantLookup:       "company/base:latest",
			wantBaseImageArg: "company/base:latest@sha256:base",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inspected []string
			var pulled string
			var captured map[string]*string
			m := &docker.MockDockerClient{}
			m.ImageInspectFn = func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
				inspected = append(inspected, ref)
				if ref == tc.wantLookup && pulled == "" {
					return client.ImageInspectResult{}, errors.New("no such image")
				}
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			m.ImagePullFn = func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
				pulled = ref
				return docker.PullResponse{Body: strings.NewReader("")}, nil
			}
			m.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
				captured = opts.BuildArgs
				return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}
			docker.WithDockerMock(t, m)

			if _, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
				tc.project,
				"test-project",
				BuildOptions{Force: true, AgentVersion: "1.2.3"},
				&termio.Mock{},
			); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			lookedUp := false
			for _, ref := range inspected {
				if ref == tc.wantLookup {
					lookedUp = true
				}
			}
			if !lookedUp {
				t.Errorf("base image inspected as %q, want %q", inspected, tc.wantLookup)
			}
			if pulled != tc.wantLookup {
				t.Errorf("ImagePull ref = %q, want %q", pulled, tc.wantLookup)
			}
			if base := captured["BASE_IMAGE"]; base == nil || *base != tc.wantBaseImageArg {
				t.Errorf("BASE_IMAGE build arg = %v, want %q", base, tc.wantBaseImageArg)
			}
		})
	}
}

func TestEnsureImageReadsVersionAndEnvFromDocker(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	orig := defaultAgentVersionResolver
	defaultAgentVersionResolver = func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "2.0.0", nil
	}
	t.Cleanup(func() { defaultAgentVersionResolver = orig })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     "sha256:abc123",
				Config: dockerConfigWith("2.0.0", []string{"PATH=/usr/bin"}),
			}, nil
		},
	})

	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: false},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Fatalf("expected no microsandbox load from EnsureImage, got %d", len(msbClient.LoadedImages))
	}
	if info.Env["PATH"] != "/usr/bin" {
		t.Errorf("info.Env = %v", info.Env)
	}
}

func TestEnsureImageReturnsDigestImageRefAsTag(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env: []string{"PATH=/usr/bin"},
				},
			}, nil
		},
	})
	info, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: false},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "agents-sandbox/runner-test-project:opencode-latest"
	if info.DockerTag != want {
		t.Errorf("info.DockerTag = %q, want agent-based image ref %q", info.DockerTag, want)
	}
}

func TestEnsureImageReadsVersionFromDocker(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	orig := defaultAgentVersionResolver
	defaultAgentVersionResolver = func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "3.0.0", nil
	}
	t.Cleanup(func() { defaultAgentVersionResolver = orig })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     "sha256:abc123",
				Config: dockerConfigWith("3.0.0", nil),
			}, nil
		},
	})

	msbClient := &msb.MockMsbClient{}

	info, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: false},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load, got %d loads", len(msbClient.LoadedImages))
	}
	if info.Env == nil {
		t.Error("expected a non-nil env map from the built image")
	}
}

// TestEnsureImageNoRebuildReusesUnlabeledImageOffline pins that --no-rebuild
// reuses an image without an agent-version label without looking up the latest
// release: the lookup needs the network, and a reused image never uses it.
func TestEnsureImageNoRebuildReusesUnlabeledImageOffline(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersionResolver(t, func(context.Context, agent.Agent, string) (string, error) {
		return "", errors.New("offline")
	})
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: map[string]string{dockerfileIDLabelKey: "legacy"}},
			}, nil
		},
		ImageBuildFn: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
			t.Fatal("--no-rebuild must not build a present image")
			return client.ImageBuildResult{}, nil
		},
	})

	info, err := EnsureImage(context.Background(), docker.Get(), configpaths.Get(), agentOpencode(t),
		"proj",
		nil,
		BuildOptions{NoRebuild: true},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if info.Digest != "sha256:existing" {
		t.Errorf("Digest = %q, want the existing image", info.Digest)
	}
}

// TestEnsureImageWarnsIgnoredWorktreeConfig pins that the exported EnsureImage
// entry still warns about an ignored worktree-level .agents-sandbox. The warning
// runs before any docker work, so a docker failure is immaterial to the assert.
func TestEnsureImageWarnsIgnoredWorktreeConfig(t *testing.T) {
	synthesizeBareWorktree(t, true)
	docker.WithDefaultErrorDockerMock(t)
	ui := &termio.Mock{}

	_, _ = EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		agentOpencode(t),
		"slug",
		nil,
		BuildOptions{AgentVersion: testAgentVersion},
		ui,
	)

	assertWarnsIgnoredWorktreeConfig(t, ui)
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

func TestBuildLoadsTheEnsuredImageIntoMicrosandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     "sha256:abc123",
				Config: dockerConfigWith("1.2.3", []string{"PATH=/usr/bin"}),
			}, nil
		},
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(io.LimitReader(nil, 0)), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	if _, err := Build(
		context.Background(),
		docker.Get(),
		msbClient,
		configpaths.Get(),
		a,
		"test-project",
		nil,
		BuildOptions{},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Errorf("expected 1 image load, got %d", len(msbClient.LoadedImages))
	}
}

// TestBuildFailsWhenEnsureOrLoadFails pins that Build reports a failed image
// build and a failed load into microsandbox.
func TestBuildFailsWhenEnsureOrLoadFails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T)
		opts  BuildOptions
	}{
		{
			name:  "image build fails",
			setup: docker.WithDefaultErrorDockerMock,
			opts:  BuildOptions{Force: true},
		},
		{
			name: "load into microsandbox fails",
			setup: func(t *testing.T) {
				t.Helper()
				docker.WithDockerMock(t, &docker.MockDockerClient{
					ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
						return client.ImageInspectResult{ID: "sha256:abc123", Config: dockerConfigWith("1.2.3", nil)}, nil
					},
					ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
						return nil, errors.New("docker save failed")
					},
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			withMockAgentVersion(t, "1.2.3")
			tc.setup(t)
			msbClient := &msb.MockMsbClient{
				ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
			}
			if _, err := Build(context.Background(), docker.Get(), msbClient, configpaths.Get(),
				agentOpencode(t), "test-project", nil, tc.opts, &termio.Mock{},
			); err == nil {
				t.Error("Build error = nil, want a failure")
			}
		})
	}
}

// TestBuildWarnsIgnoredWorktreeConfig pins the same warning on the Build entry.
func TestBuildWarnsIgnoredWorktreeConfig(t *testing.T) {
	synthesizeBareWorktree(t, true)
	docker.WithDefaultErrorDockerMock(t)
	ui := &termio.Mock{}

	_, _ = Build(
		context.Background(),
		docker.Get(),
		&msb.MockMsbClient{},
		configpaths.Get(),
		agentOpencode(t),
		"slug",
		nil,
		BuildOptions{AgentVersion: testAgentVersion},
		ui,
	)

	assertWarnsIgnoredWorktreeConfig(t, ui)
}

// testDockerImageID is a full sha256 image ID whose 16-char prefix keys the
// content-addressed reference in tests.
const testDockerImageID = "sha256:9f2c1a7b4d6e0f3199999999999999999999999999999999999999999999999999"

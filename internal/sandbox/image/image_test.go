package image

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestReferencesImageDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest\nRUN echo hi\n")
	if !referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true for Dockerfile with base image")
	}
}

func TestReferencesImageReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false for non-base Dockerfile")
	}
}

func TestReferencesImageIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM agents-sandbox/runner-base:latest\nFROM debian:trixie-slim\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false for commented FROM")
	}
}

func TestReferencesImageMatchesImageIdentifierRegardlessOfTag(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:custom\nRUN echo hi\n")
	if !referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true for base image with a non-default tag")
	}
}

func TestReferencesImageMatchesMainImageWithStageAlias(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest AS builder\nRUN echo hi\n")
	if !referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true for base image declared as a stage")
	}
}

func TestReferencesImageIgnoresStageReference(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim AS base\nFROM base AS builder\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false when base image is used as a stage reference")
	}
}

func TestReferencesImageIgnoresBuildFlag(t *testing.T) {
	dockerfile := []byte("FROM --platform=linux/amd64 agents-sandbox/runner-base:latest\nRUN echo hi\n")
	if !referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true for base image after build flag")
	}
}

func TestReferencesImageFalseWhenBaseNotLastStage(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest AS base\nFROM debian:trixie-slim AS final\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false when base image is not the last stage")
	}
}

func TestReferencesImageTrueWhenBaseIsLastStageViaAlias(t *testing.T) {
	dockerfile := []byte(
		"FROM debian:trixie-slim AS base\nFROM agents-sandbox/runner-base:latest AS builder\nFROM builder\n",
	)
	if !referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true when base image backs the final stage through an alias")
	}
}

func TestReferencesImageIgnoresIntermediateStage(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest AS base\nFROM debian:trixie-slim AS final\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false for base image used only as an intermediate stage")
	}
}

func TestReferencesImageTrueWhenLastStageReusesAliasOfBase(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest AS base\nFROM base\n")
	if !referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=true when the last stage reuses an alias of the base image")
	}
}

func TestReferencesImageDetectsDindImage(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	if !referencesImage(dockerfile, "agents-sandbox/runner-base-dind") {
		t.Error("expected referencesImage=true for Dockerfile with dind FROM")
	}
}

func TestReferencesImageReturnsFalseForPlainBase(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base:latest\nRUN echo hi\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base-dind") {
		t.Error("expected referencesImage=false for plain base Dockerfile")
	}
}

func TestReferencesImageReturnsFalseForDindOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base-dind") {
		t.Error("expected referencesImage=false for non-base Dockerfile")
	}
}

func TestReferencesImageIgnoresDindComment(t *testing.T) {
	dockerfile := []byte("# FROM agents-sandbox/runner-base-dind:latest\nFROM debian:trixie-slim\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base-dind") {
		t.Error("expected referencesImage=false for commented FROM")
	}
}

func TestReferencesImageDindDoesNotMatchBase(t *testing.T) {
	dockerfile := []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	if referencesImage(dockerfile, "agents-sandbox/runner-base") {
		t.Error("expected referencesImage=false for dind Dockerfile with base image identifier (no false positive)")
	}
}

func TestImageTag(t *testing.T) {
	got := runnerTag("myproj-aBc1234D", "opencode")
	expected := "agents-sandbox/runner-myproj-aBc1234D:opencode-latest"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestWriteDockerfileContextContents(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	dir := t.TempDir()
	if err := writeDockerfileContext(dir, dockerfile); err != nil {
		t.Fatalf("writeDockerfileContext: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if string(got) != string(dockerfile) {
		t.Error("Dockerfile content does not match")
	}
	script, err := os.ReadFile(filepath.Join(dir, pathMergeAsset))
	if err != nil {
		t.Fatalf("read %s: %v", pathMergeAsset, err)
	}
	if string(script) != string(pathMergeScript()) {
		t.Errorf("%s content does not match the embedded script", pathMergeAsset)
	}
	for _, name := range []string{"Dockerfile", pathMergeAsset} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != dockerfileMode {
			t.Errorf("%s mode = %v, want %v", name, info.Mode().Perm(), os.FileMode(dockerfileMode))
		}
	}
}

func TestEnsureImageBuildsViaBuilder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not found")
		},
	})
	mock := fakeBuilder(t, builder.BuildResult{
		ArchivePath: "/tmp/archive.tar",
		Digest:      "sha256:new",
		Env:         []string{"PATH=/usr/bin"},
	}, nil)

	info, err := EnsureImageWithClient(
		context.Background(),
		a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("builder calls = %d, want 1", len(mock.Calls))
	}
	if info.ArchivePath != "/tmp/archive.tar" {
		t.Errorf("ArchivePath = %q, want the builder archive", info.ArchivePath)
	}
	if info.Digest != "sha256:new" {
		t.Errorf("Digest = %q, want sha256:new", info.Digest)
	}
	if info.Env["PATH"] != "/usr/bin" {
		t.Errorf("Env = %v, want PATH=/usr/bin", info.Env)
	}
}

func TestEnsureImageSkipsBuildOnCacheHit(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(renderBytes(t, a, nil, false), "1.2.3"),
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return msbImageConfig("sha256:cached", labels, []string{"PATH=/usr/bin"}), nil
		},
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/unused"}, nil)

	info, err := EnsureImageWithClient(
		context.Background(), a, nil, "test-project", BuildOptions{}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Errorf("builder must not be called on a cache hit, got %d calls", len(mock.Calls))
	}
	if info.ArchivePath != "" {
		t.Errorf("ArchivePath = %q, want empty on a cache hit", info.ArchivePath)
	}
	if info.Digest != "sha256:cached" {
		t.Errorf("Digest = %q, want the cached msb digest", info.Digest)
	}
	if info.Env["PATH"] != "/usr/bin" {
		t.Errorf("Env = %v, want the cached msb env", info.Env)
	}
}

func TestEnsureImageBuildPassesBuilderRequest(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not found")
		},
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	if _, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"), "test-project",
		BuildOptions{Force: true, AgentVersion: "1.2.3"}, &termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	req := mock.Calls[0]
	if req.Tag != runnerTag("test-project", a.Name()) {
		t.Errorf("Tag = %q, want the runner tag", req.Tag)
	}
	if req.BaseRef != "debian:trixie-slim" {
		t.Errorf("BaseRef = %q, want debian:trixie-slim", req.BaseRef)
	}
	if req.Platform != builder.HostPlatform() {
		t.Errorf("Platform = %q, want %q", req.Platform, builder.HostPlatform())
	}
	if !req.NoCache {
		t.Error("NoCache = false, want true for a forced rebuild")
	}
	if req.ContextDir == "" {
		t.Error("ContextDir is empty")
	}
	if req.BuildArgs["OPENCODE_VERSION"] != "1.2.3" {
		t.Errorf("OPENCODE_VERSION = %q, want 1.2.3", req.BuildArgs["OPENCODE_VERSION"])
	}
	if _, ok := req.BuildArgs["BASE_IMAGE"]; ok {
		t.Error("BASE_IMAGE must be supplied by the builder, not the image package")
	}
	if req.BuildArgs["DOCKERFILE_ID"] == "" {
		t.Error("DOCKERFILE_ID build arg missing")
	}
}

func TestEnsureImageBuildArgsCarryHostUser(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not found")
		},
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	if _, err := EnsureImageWithClient(
		context.Background(), a, nil, "test-project", BuildOptions{Force: true}, &termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	args := mock.Calls[0].BuildArgs
	if args["USER_UID"] != strconv.Itoa(os.Getuid()) {
		t.Errorf("USER_UID = %q, want %d", args["USER_UID"], os.Getuid())
	}
	if args["USER_GID"] != strconv.Itoa(os.Getgid()) {
		t.Errorf("USER_GID = %q, want %d", args["USER_GID"], os.Getgid())
	}
}

func TestEnsureImageBuildNoCacheFalseWithoutForce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not found")
		},
	})
	mock := fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	if _, err := EnsureImageWithClient(
		context.Background(), a, nil, "test-project", BuildOptions{}, &termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if mock.Calls[0].NoCache {
		t.Error("NoCache = true, want false when building without force")
	}
}

func TestEnsureImageBuildForwardsProgress(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not found")
		},
	})
	builder.WithMockBuilder(t, &builder.MockBuilder{
		BuildFn: func(_ context.Context, req builder.BuildRequest) (builder.BuildResult, error) {
			req.Progress("Step 1/1 : FROM x")
			return builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil
		},
	})
	ui := &termio.Mock{}
	if _, err := EnsureImageWithClient(
		context.Background(), a, nil, "test-project", BuildOptions{}, ui,
	); err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if !verboseContains(ui, "Step 1/1 : FROM x") {
		t.Errorf("verbose calls = %v, want the forwarded build progress line", ui.VerboseCalls)
	}
}

func TestEnsureImageCacheHitInspectError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(renderBytes(t, a, nil, false), "1.2.3"),
	}
	calls := 0
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			calls++
			if calls == 1 {
				return msbImageConfig("sha256:cached", labels, nil), nil
			}
			return nil, errors.New("inspect boom")
		},
	})
	fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/unused"}, nil)

	_, err := EnsureImageWithClient(
		context.Background(), a, nil, "test-project", BuildOptions{}, &termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "cannot inspect cached image") {
		t.Fatalf("error = %v, want a cannot-inspect-cached-image error", err)
	}
}

func TestEnsureImageReadsEnvFromBuildResult(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "2.0.0", nil
	})
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not found")
		},
	})
	fakeBuilder(t, builder.BuildResult{
		ArchivePath: "/tmp/a",
		Digest:      "sha256:abc123",
		Env:         []string{"PATH=/usr/bin"},
	}, nil)

	info, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"), "test-project",
		BuildOptions{}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if info.Env["PATH"] != "/usr/bin" {
		t.Errorf("info.Env = %v, want PATH=/usr/bin", info.Env)
	}
	if info.Digest != "sha256:abc123" {
		t.Errorf("info.Digest = %q, want sha256:abc123", info.Digest)
	}
}

func TestEnsureImageReturnsRunnerTag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(renderBytes(t, a, nil, false), "1.2.3"),
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return msbImageConfig("sha256:abc123", labels, []string{"PATH=/usr/bin"}), nil
		},
	})
	info, err := EnsureImageWithClient(
		context.Background(), a, nil, "test-project",
		BuildOptions{}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	want := "agents-sandbox/runner-test-project:opencode-latest"
	if info.Tag != want {
		t.Errorf("info.Tag = %q, want agent-based image ref %q", info.Tag, want)
	}
}

func TestEnsureImageDoesNotLoadIntoMSB(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msbClient := &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not in cache")
		},
	}
	msb.WithMsbMock(t, msbClient)
	fakeBuilder(t, builder.BuildResult{
		ArchivePath: "/tmp/a", Digest: "sha256:abc123", Env: []string{"PATH=/usr/bin"},
	}, nil)

	_, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM agents-sandbox/runner-base:latest\nRUN echo hi\n"),
		"test-project", BuildOptions{}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("EnsureImage must not load into microsandbox; got %d loads", len(msbClient.LoadedImages))
	}
}

func TestEnsureImageReturnsErrorWhenBuildFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not found")
		},
	})
	fakeBuilder(t, builder.BuildResult{}, errors.New("build boom"))

	_, err := EnsureImageWithClient(
		context.Background(), a, nil, "test-project", BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil {
		t.Error("expected error when the builder fails")
	}
}

func TestEnsureLoadedLoadsArchive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(archivePath, []byte("tar-data"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	mclient := &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return errors.New("not cached") },
	}
	if err := EnsureLoaded(
		context.Background(), mclient, archivePath, "agents-sandbox/runner-p:opencode", &termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(mclient.LoadedImages) != 1 || mclient.LoadedImages[0] != "agents-sandbox/runner-p:opencode" {
		t.Errorf("LoadedImages = %v, want the runner ref", mclient.LoadedImages)
	}
}

func TestEnsureLoadedSkipsWhenCached(t *testing.T) {
	mclient := &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return nil },
	}
	if err := EnsureLoaded(context.Background(), mclient, "", "ref", &termio.Mock{}); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(mclient.LoadedImages) != 0 {
		t.Errorf("expected no load when cached, got %v", mclient.LoadedImages)
	}
}

func TestEnsureLoadedErrorsWithoutArchive(t *testing.T) {
	mclient := &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return errors.New("not cached") },
	}
	err := EnsureLoaded(context.Background(), mclient, "", "ref", &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "archive") {
		t.Fatalf("EnsureLoaded error = %v, want a missing-archive error", err)
	}
	if len(mclient.LoadedImages) != 0 {
		t.Errorf("expected no load when the archive is missing, got %v", mclient.LoadedImages)
	}
}

func TestBuildDecisionReadsMsbCache(t *testing.T) {
	labels := map[string]string{dockerfileIDLabelKey: "abc"}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return msbImageConfig("", labels, nil), nil
		},
	})
	if needs, _ := buildDecision(context.Background(), "tag", false, "abc", imageIdentity{}); needs {
		t.Error("expected no build when the cached msb label matches")
	}

	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("not found")
		},
	})
	if needs, _ := buildDecision(context.Background(), "tag", false, "abc", imageIdentity{}); !needs {
		t.Error("expected a build when the msb image is absent")
	}
}

func TestEnsureLoadedReturnsErrorWhenLoadFails(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(archivePath, []byte("tar-data"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	mclient := &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return errors.New("not cached") },
		ImageLoadFn: func(context.Context, string, io.Reader) error {
			return errors.New("load boom")
		},
	}
	if err := EnsureLoaded(context.Background(), mclient, archivePath, "ref", &termio.Mock{}); err == nil {
		t.Fatal("expected EnsureLoaded to return an error when ImageLoad fails")
	}
}

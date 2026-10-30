package image

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// dockerConfigWith builds a Docker image config carrying the given agent label
// and environment for mocked ImageInspect results.
func dockerConfigWith(version string, env []string) *dockerspec.DockerOCIImageConfig {
	labels := map[string]string{}
	if version != "" {
		labels[agentLabelKey] = version
	}
	return &dockerspec.DockerOCIImageConfig{
		Env: env, Labels: labels,
	}
}

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

func TestImageTag(t *testing.T) {
	got := runnerTag("myproj-aBc1234D", "opencode")
	expected := "agents-sandbox/runner-myproj-aBc1234D:opencode-latest"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestBuildDockerImageSetsHostUserBuildArgs(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	m := &docker.MockDockerClient{}
	var capturedBuildArgs map[string]*string
	m.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		capturedBuildArgs = opts.BuildArgs
		return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	docker.WithDockerMock(t, m)

	a, _ := agent.Lookup("opencode")
	if err := buildImage(
		context.Background(),
		a,
		dockerfile,
		"tag",
		false,
		"",
		"debian:trixie-slim",
		"",
		false,
		imageIdentity{},
		func(string) {},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantUID := strconv.Itoa(os.Getuid())
	wantGID := strconv.Itoa(os.Getgid())
	if v := capturedBuildArgs["USER_UID"]; v == nil || *v != wantUID {
		t.Errorf("USER_UID: want %q, got %v", wantUID, v)
	}
	if v := capturedBuildArgs["USER_GID"]; v == nil || *v != wantGID {
		t.Errorf("USER_GID: want %q, got %v", wantGID, v)
	}
}

func TestBuildImageNoCacheFollowsParameter(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	a, _ := agent.Lookup("opencode")

	for _, noCache := range []bool{true, false} {
		t.Run(strconv.FormatBool(noCache), func(t *testing.T) {
			m := &docker.MockDockerClient{}
			var capturedNoCache bool
			m.ImageBuildFn = func(
				_ context.Context, _ io.Reader, opts client.ImageBuildOptions,
			) (client.ImageBuildResult, error) {
				capturedNoCache = opts.NoCache
				return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}
			docker.WithDockerMock(t, m)

			if err := buildImage(
				context.Background(), a, dockerfile, "tag",
				noCache, "", "debian:trixie-slim", "", false, imageIdentity{}, func(string) {},
			); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if capturedNoCache != noCache {
				t.Errorf("NoCache = %v, want %v", capturedNoCache, noCache)
			}
		})
	}
}

func TestDockerfileTarContents(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	var tarBuf bytes.Buffer
	if err := writeDockerfileTar(&tarBuf, dockerfile); err != nil {
		t.Fatalf("writeDockerfileTar failed: %v", err)
	}

	entries := map[string]string{}
	var names []string
	tr := tar.NewReader(&tarBuf)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry: %v", err)
		}
		names = append(names, header.Name)
		entries[header.Name] = string(content)
	}
	if len(names) == 0 || names[0] != "Dockerfile" {
		t.Errorf("first tar entry = %v, want Dockerfile first", names)
	}
	if !bytes.Equal([]byte(entries["Dockerfile"]), dockerfile) {
		t.Error("Dockerfile tar content does not match")
	}
	if got := entries[pathMergeAsset]; got != string(pathMergeScript()) {
		t.Errorf("tar entry %q = %q, want embedded script", pathMergeAsset, got)
	}
}

// failAfterBytes fails the first write once limit bytes have already been
// written, reaching the header and content write error paths in
// writeDockerfileTar (a bytes.Buffer never fails). A tar header block is 512
// bytes, so limit 0 fails the header and limit 512 the content.
type failAfterBytes struct {
	limit int
	seen  int
}

func (w *failAfterBytes) Write(p []byte) (int, error) {
	if w.seen >= w.limit {
		return 0, errors.New("synthetic write failure")
	}
	w.seen += len(p)
	return len(p), nil
}

// failOnCall fails the nth write, reaching the tar close (trailer) error path.
type failOnCall struct {
	failOn int
	calls  int
}

func (w *failOnCall) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.failOn {
		return 0, errors.New("synthetic write failure")
	}
	return len(p), nil
}

// callCounter records how many write calls a tar stream makes.
type callCounter struct{ calls int }

func (w *callCounter) Write(p []byte) (int, error) {
	w.calls++
	return len(p), nil
}

func TestWriteDockerfileTarWriteErrors(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")

	counter := &callCounter{}
	if err := writeDockerfileTar(counter, dockerfile); err != nil {
		t.Fatalf("baseline writeDockerfileTar: %v", err)
	}

	// A tar header is 512 bytes and the content is padded to a block boundary,
	// so the first build-context file starts at this offset. Failing there
	// exercises the per-file error path, which the Dockerfile cases below do not
	// reach.
	assetOffset := 512 + (len(dockerfile)+511)/512*512

	cases := []struct {
		name    string
		writer  io.Writer
		wantErr string
	}{
		{"header write fails", &failAfterBytes{limit: 0}, "tar write header"},
		{"content write fails", &failAfterBytes{limit: 512}, "tar write Dockerfile"},
		{"asset header write fails", &failAfterBytes{limit: assetOffset}, "tar write header"},
		{"asset content write fails", &failAfterBytes{limit: assetOffset + 512}, "tar write " + pathMergeAsset},
		{"close write fails", &failOnCall{failOn: counter.calls}, "tar close"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := writeDockerfileTar(tc.writer, dockerfile)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("writeDockerfileTar error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestScanBuildOutputForwardsStreamLines(t *testing.T) {
	body := strings.NewReader(`{"stream":"Step 1/1 : FROM debian\n"}
{"stream":"\n"}
{"stream":"Successfully built abc123\n"}`)

	var got []string
	if err := scanBuildOutput(body, func(s string) { got = append(got, s) }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"Step 1/1 : FROM debian", "Successfully built abc123"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected lines %q, got %q", want, got)
	}
}

func TestScanBuildOutputReturnsErrorMessage(t *testing.T) {
	body := strings.NewReader(
		`{"errorDetail":{"message":"pull access denied for base"},"error":"pull access denied for base"}`,
	)

	err := scanBuildOutput(body, func(string) {})
	if err == nil {
		t.Fatal("expected scanBuildOutput to return an error")
	}
	if !strings.Contains(err.Error(), "pull access denied") {
		t.Errorf("expected error to mention the build failure, got %q", err)
	}
}

func TestEnsureImageReturnsErrorWhenBuildFails(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	l := &termio.Mock{}
	docker.WithDefaultErrorDockerMock(t)
	_, err := EnsureImageWithClient(
		context.Background(),
		a,
		nil,
		"test-project",
		BuildOptions{Force: true},
		l,
	)
	if err == nil {
		t.Error("expected error when Docker build fails")
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

func TestEnsureImageDoesNotCreateDigestAliasTag(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
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
	_, err := EnsureImageWithClient(
		context.Background(),
		a,
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
	WithMockAgentVersion(t, "1.2.3")
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
	_, err := EnsureImageWithClient(
		context.Background(),
		a,
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

func TestBuildImagePassesAgentVersionBuildArg(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	orig := resolveAgentVersion
	resolveAgentVersion = func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		return requested, nil
	}
	t.Cleanup(func() { resolveAgentVersion = orig })

	m := &docker.MockDockerClient{}
	var captured map[string]*string
	m.ImageInspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{ID: "sha256:base"}, nil
	}
	m.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		captured = opts.BuildArgs
		return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	docker.WithDockerMock(t, m)

	_, err := EnsureImageWithClient(
		context.Background(),
		a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: true, AgentVersion: "1.2.3"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected build args to be captured")
	}
	v := captured["OPENCODE_VERSION"]
	if v == nil || *v != "1.2.3" {
		t.Errorf("OPENCODE_VERSION build arg = %v, want %q", v, "1.2.3")
	}
	if base := captured["BASE_IMAGE"]; base == nil || *base != "debian:trixie-slim@sha256:base" {
		t.Errorf("BASE_IMAGE build arg = %v, want %q", base, "debian:trixie-slim@sha256:base")
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
	orig := resolveAgentVersion
	resolveAgentVersion = func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		return requested, nil
	}
	t.Cleanup(func() { resolveAgentVersion = orig })

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

			if _, err := EnsureImageWithClient(
				context.Background(),
				a,
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
	orig := resolveAgentVersion
	resolveAgentVersion = func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "2.0.0", nil
	}
	t.Cleanup(func() { resolveAgentVersion = orig })

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

	info, err := EnsureImageWithClient(
		context.Background(),
		a,
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
	WithMockAgentVersion(t, "1.2.3")
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
	info, err := EnsureImageWithClient(
		context.Background(),
		a,
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
	orig := resolveAgentVersion
	resolveAgentVersion = func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "3.0.0", nil
	}
	t.Cleanup(func() { resolveAgentVersion = orig })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     "sha256:abc123",
				Config: dockerConfigWith("3.0.0", nil),
			}, nil
		},
	})

	msbClient := &msb.MockMsbClient{}

	info, err := EnsureImageWithClient(
		context.Background(),
		a,
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

// TestEnsureLoadedDecision pins the load decision table: the content digest is
// authoritative, and a cached image that records no digest is unknown content
// that must be reloaded. The dockerfile-id label is never trusted as a
// substitute for the digest, and host Docker is never inspected.
func TestEnsureLoadedDecision(t *testing.T) {
	cases := []struct {
		name          string
		cached        bool
		msbDigest     string
		msbLabels     map[string]string
		info          ImageInfo
		wantLoadCount int
	}{
		{
			name:          "not cached loads",
			cached:        false,
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			wantLoadCount: 1,
		},
		{
			name:          "equal digest skips",
			cached:        true,
			msbDigest:     "sha256:new",
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			wantLoadCount: 0,
		},
		{
			name:          "different digest loads",
			cached:        true,
			msbDigest:     "sha256:old",
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			wantLoadCount: 1,
		},
		{
			name:          "no digest must reload even when the label matches",
			cached:        true,
			msbLabels:     map[string]string{dockerfileIDLabelKey: "abc"},
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			wantLoadCount: 1,
		},
		{
			name:          "no digest, different label loads",
			cached:        true,
			msbLabels:     map[string]string{dockerfileIDLabelKey: "old"},
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			wantLoadCount: 1,
		},
		{
			name:          "different digest with equal label loads",
			cached:        true,
			msbDigest:     "sha256:old",
			msbLabels:     map[string]string{dockerfileIDLabelKey: "abc"},
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			wantLoadCount: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			hostInspected := false
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(
					_ context.Context, _ string, _ ...client.ImageInspectOption,
				) (client.ImageInspectResult, error) {
					hostInspected = true
					return client.ImageInspectResult{}, nil
				},
				ImageSaveFn: func(
					_ context.Context, _ []string, _ ...client.ImageSaveOption,
				) (client.ImageSaveResult, error) {
					return io.NopCloser(strings.NewReader("tar-data")), nil
				},
			})
			msbClient := &msb.MockMsbClient{
				ImageGetFn: func(_ context.Context, _ string) error {
					if tc.cached {
						return nil
					}
					return errors.New("not cached")
				},
				ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
					return &msbSdk.ImageConfig{Digest: tc.msbDigest, Labels: tc.msbLabels}, nil
				},
			}
			if _, err := EnsureLoaded(context.Background(), msbClient, tc.info, &termio.Mock{}); err != nil {
				t.Fatalf("EnsureLoaded: %v", err)
			}
			if hostInspected {
				t.Error("EnsureLoaded must decide from the microsandbox cache alone, never inspecting host Docker")
			}
			if len(msbClient.LoadedImages) != tc.wantLoadCount {
				t.Errorf(
					"load count = %d, want %d (%v)",
					len(msbClient.LoadedImages), tc.wantLoadCount, msbClient.LoadedImages,
				)
			}
		})
	}
}

func TestEnsureLoadedReloadsStaleContentUnderSameRef(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, refs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			if len(refs) != 1 || refs[0] != rTag {
				t.Errorf("ImageSave refs = %v, want %q", refs, rTag)
			}
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	removeCalled := false
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:msb-old"}, nil
		},
		ImageRemoveFn: func(_ context.Context, _ string, _ bool) error {
			removeCalled = true
			return errors.New("database error: FOREIGN KEY constraint failed")
		},
	}
	ui := &termio.Mock{}
	info := ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:docker-new", DockerfileID: "abc"}
	if _, err := EnsureLoaded(context.Background(), msbClient, info, ui); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if removeCalled {
		t.Error("expected the stale cached image to be refreshed without removal")
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != rTag {
		t.Errorf("LoadedImages = %v, want a reload of %q", msbClient.LoadedImages, rTag)
	}
}

func TestEnsureLoadedSkipsWhenCachedContentMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	removed := false
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
		ImageRemoveFn: func(_ context.Context, _ string, _ bool) error { removed = true; return nil },
	}
	info := ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:same"}
	if _, err := EnsureLoaded(context.Background(), msbClient, info, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if removed {
		t.Error("expected the matching cached image not to be removed")
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no reload when content matches, got %v", msbClient.LoadedImages)
	}
}

// TestEnsureLoadedReloadsLegacyImageWithoutDigest pins the cost of treating a
// missing digest as unknown content: a legacy cached image whose dockerfile-id
// label matches is still reloaded, so the fallback can never let a label match
// stand in for a content comparison. The load records a digest, so the reload
// happens at most once.
func TestEnsureLoadedReloadsLegacyImageWithoutDigest(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	labels := map[string]string{dockerfileIDLabelKey: "abc123"}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "", Labels: labels}, nil
		},
	}
	info := ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc123"}
	if _, err := EnsureLoaded(context.Background(), msbClient, info, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Errorf("expected a reload when the cached image records no digest, got %v", msbClient.LoadedImages)
	}
}

func TestEnsureLoadedLoadsWhenNotCached(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, refs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			if len(refs) != 1 || refs[0] != "agents-sandbox/runner-test-project:abc" {
				t.Errorf("ImageSave refs = %v, want runner tag %q", refs, "agents-sandbox/runner-test-project:abc")
			}
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})

	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	if _, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{
			DockerTag: "agents-sandbox/runner-test-project:abc",
			Ref:       "agents-sandbox/runner-test-project:abc",
			Digest:    "sha256:new",
		},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Fatalf("expected 1 image load, got %d", len(msbClient.LoadedImages))
	}
	if msbClient.LoadedImages[0] != "agents-sandbox/runner-test-project:abc" {
		t.Errorf(
			"loaded image ref = %q, want %q",
			msbClient.LoadedImages[0],
			"agents-sandbox/runner-test-project:abc",
		)
	}
}

func TestEnsureLoadedReturnsErrorWhenSaveFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, errors.New("docker save failed")
		},
	})

	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	_, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{
			DockerTag: "agents-sandbox/runner-test-project:abc",
			Ref:       "agents-sandbox/runner-test-project:abc",
			Digest:    "sha256:new",
		},
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error when Docker save fails")
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load on save failure, got %d", len(msbClient.LoadedImages))
	}
}

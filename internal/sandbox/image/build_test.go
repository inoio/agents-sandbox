package image

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
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
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestScanFromStagesLowercaseInstructions(t *testing.T) {
	lastImage, stageBase := scanFromStages([]byte(
		"from golang:1.24 as build\nrun go build\nFrom debian:trixie-slim AS final\n",
	))
	if lastImage != "debian:trixie-slim" {
		t.Errorf("lastImage = %q, want debian:trixie-slim", lastImage)
	}
	if got := stageBase["build"]; got != "golang:1.24" {
		t.Errorf("stageBase[build] = %q, want golang:1.24", got)
	}
	if got := stageBase["final"]; got != "debian:trixie-slim" {
		t.Errorf("stageBase[final] = %q, want debian:trixie-slim", got)
	}
}

func TestResolveStageBaseReturnsUnresolvedToken(t *testing.T) {
	if got := resolveStageBase("debian", map[string]string{"builder": "base"}); got != "debian" {
		t.Errorf(`resolveStageBase("debian", {builder: base}) = %q, want "debian"`, got)
	}
}

func TestResolveStageBaseStopsOnCyclicAlias(t *testing.T) {
	// The token the walk stops at is observable, not just "did it terminate":
	// a self-alias stops at "0", and a two-stage cycle entered at "b" stops at
	// "b". Asserting the value keeps the cycle guard's return honest.
	if got := resolveStageBase("0", map[string]string{"0": "0"}); got != "0" {
		t.Errorf(`resolveStageBase("0", {0: 0}) = %q, want "0"`, got)
	}
	if got := resolveStageBase("b", map[string]string{"a": "b", "b": "a"}); got != "b" {
		t.Errorf(`resolveStageBase("b", {a: b, b: a}) = %q, want "b"`, got)
	}
}

func TestParseFromLowercaseKeywordAndAlias(t *testing.T) {
	image, alias := parseFrom("from --platform=linux/amd64 debian:trixie-slim as build")
	if image != "debian:trixie-slim" {
		t.Errorf("image = %q, want debian:trixie-slim", image)
	}
	if alias != "build" {
		t.Errorf("alias = %q, want build", alias)
	}
}

func TestParseFromIgnoresNonFromInstruction(t *testing.T) {
	if image, alias := parseFrom("RUN echo hi"); image != "" || alias != "" {
		t.Errorf("parseFrom(RUN ...) = (%q, %q), want empty", image, alias)
	}
}

// TestParseFromTrimsCRLF pins the trailing-whitespace trim of a CRLF Dockerfile
// line. Without it the alias keeps a trailing CR, which no longer matches the
// alias the line-buffered scanner sees, so a custom CRLF base fails the build.
func TestParseFromTrimsCRLF(t *testing.T) {
	image, alias := parseFrom("FROM ubuntu:24.04 AS base\r")
	if image != "ubuntu:24.04" || alias != "base" {
		t.Errorf("parseFrom(CRLF line) = (%q, %q), want (ubuntu:24.04, base)", image, alias)
	}
}

func TestParseFromIgnoresFromage(t *testing.T) {
	if image, _ := parseFrom("FROMAGE debian:trixie-slim"); image != "" {
		t.Errorf("parseFrom(FROMAGE ...) image = %q, want empty", image)
	}
}

func TestBuildDockerImageError(t *testing.T) {
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, errors.New("build boom")
		},
	})
	err := buildDockerImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
		"tag", "label", false, &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected buildDockerImage to return an error when the build fails")
	}
}

func TestBuildDockerImageForwardsStreamLines(t *testing.T) {
	a := agentOpencode(t)
	ui := &termio.Mock{}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{
				Body: io.NopCloser(strings.NewReader("{\"stream\":\"Step 1/1 : FROM debian\\n\"}\n")),
			}, nil
		},
	})
	if err := buildDockerImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
		"tag", "label", false, ui,
	); err != nil {
		t.Fatalf("buildDockerImage: %v", err)
	}
	var found bool
	for _, c := range ui.VerboseCalls {
		if c == "Step 1/1 : FROM debian" {
			found = true
		}
	}
	if !found {
		t.Errorf("build output stream line not forwarded to verbose output; got %v", ui.VerboseCalls)
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
		docker.Get(),
		a,
		testBuildRequest(dockerfile, "", "debian:trixie-slim", "", imageIdentity{}),
		"tag",
		false,
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

func TestBuildImageReturnsErrorOnImageBuild(t *testing.T) {
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, errors.New("image build failed")
		},
	})
	err := buildImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
		"tag", false, func(string) {},
	)
	if err == nil || !strings.Contains(err.Error(), "docker image build failed") {
		t.Errorf("buildImage error = %v, want a docker build failure", err)
	}
}

func TestBuildImageDetectsPullAccessDenied(t *testing.T) {
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{
				Body: io.NopCloser(strings.NewReader(
					`{"errorDetail":{"message":"pull access denied for base"},"error":"pull access denied for base"}`,
				)),
			}, nil
		},
	})
	err := buildImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
		"tag", false, func(string) {},
	)
	if err == nil || !strings.Contains(err.Error(), "base image not found or not logged in") {
		t.Errorf("buildImage error = %v, want a pull-access-denied hint", err)
	}
}

func TestBuildImageReturnsGenericBuildError(t *testing.T) {
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{
				Body: io.NopCloser(strings.NewReader(`{"errorDetail":{"message":"some other failure"}}`)),
			}, nil
		},
	})
	err := buildImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
		"tag", false, func(string) {},
	)
	if err == nil || !strings.Contains(err.Error(), "some other failure") ||
		strings.Contains(err.Error(), "not found or not logged in") {
		t.Errorf("buildImage error = %v, want a generic build failure wrapping the stream error", err)
	}
}

// TestBuildImageHintsTroubleshootingOnUnresolvedName pins the podman failure
// mode that is not an engine bug: an image name with no registry, which podman
// refuses to resolve when it has no search registries. The error must point at
// the troubleshooting section naming that fix. The real message wraps the name
// and the config path in quotes; the classification only needs these two
// phrases.
func TestBuildImageHintsTroubleshootingOnUnresolvedName(t *testing.T) {
	a := agentOpencode(t)
	const engineError = "short-name debian:trixie-slim did not resolve to an alias and no " +
		"unqualified search registries are defined in /etc/containers/registries.conf"
	for _, tc := range []struct {
		name  string
		build func() (client.ImageBuildResult, error)
	}{
		{
			name: "streamed build error",
			build: func() (client.ImageBuildResult, error) {
				return client.ImageBuildResult{
					Body: io.NopCloser(strings.NewReader(`{"errorDetail":{"message":"` + engineError + `"}}`)),
				}, nil
			},
		},
		{
			name: "build call error",
			build: func() (client.ImageBuildResult, error) {
				return client.ImageBuildResult{}, errors.New(engineError)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageBuildFn: func(
					_ context.Context, _ io.Reader, _ client.ImageBuildOptions,
				) (client.ImageBuildResult, error) {
					return tc.build()
				},
			})
			err := buildImage(
				context.Background(), docker.Get(), a,
				testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
				"tag", false, func(string) {},
			)
			if err == nil || !strings.Contains(err.Error(), "troubleshooting.html#the-base-image-cannot-be-found") {
				t.Errorf("buildImage error = %v, want a troubleshooting pointer", err)
			}
		})
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
				context.Background(), docker.Get(), a,
				testBuildRequest(dockerfile, "", "debian:trixie-slim", "", imageIdentity{}),
				"tag",
				noCache, func(string) {},
			); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if capturedNoCache != noCache {
				t.Errorf("NoCache = %v, want %v", capturedNoCache, noCache)
			}
		})
	}
}

func TestBuildImagePassesAgentVersionBuildArg(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	orig := defaultAgentVersionResolver
	defaultAgentVersionResolver = func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		return requested, nil
	}
	t.Cleanup(func() { defaultAgentVersionResolver = orig })

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

	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
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

func TestScanBuildOutputUnexpectedOutput(t *testing.T) {
	err := scanBuildOutput(strings.NewReader("{not json"), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "unexpected Docker build output") {
		t.Errorf("scanBuildOutput error = %v, want an unexpected-output error", err)
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

func TestWriteDockerfileTarShipsHostOpenerShimExecutable(t *testing.T) {
	var buildContext bytes.Buffer
	if err := writeDockerfileTar(&buildContext, []byte("FROM scratch\n")); err != nil {
		t.Fatalf("writeDockerfileTar: %v", err)
	}
	entries, modes := readTarEntries(t, &buildContext)
	want, err := os.ReadFile(hostOpenerShimPath)
	if err != nil {
		t.Fatal(err)
	}
	if entries[hostOpenerShimAsset] != string(want) {
		t.Errorf("tar entry %q does not carry the shim", hostOpenerShimAsset)
	}
	if modes[hostOpenerShimAsset] != 0o755 {
		t.Errorf("tar entry %q mode = %o, want 755", hostOpenerShimAsset, modes[hostOpenerShimAsset])
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

// TestWriteDockerfileTarShipsTrustHelperExecutable covers the synthetic build
// context the image build consumes. The final stage COPYs the helper into the
// image and takes the executable bit from the context, so the file has to
// travel as a regular file with mode 755; a context entry the COPY cannot
// resolve fails the whole build.
func TestWriteDockerfileTarShipsTrustHelperExecutable(t *testing.T) {
	var context bytes.Buffer
	if err := writeDockerfileTar(&context, []byte("FROM scratch\n")); err != nil {
		t.Fatalf("writeDockerfileTar: %v", err)
	}

	entries, modes := readTarEntries(t, &context)

	want, err := os.ReadFile(vendoredTrustHelperPath)
	if err != nil {
		t.Fatalf("read vendored trust helper: %v", err)
	}
	if got := entries[trustHelperAsset]; got != string(want) {
		t.Errorf("tar entry %q carries %d bytes, want the vendored script's %d", trustHelperAsset, len(got), len(want))
	}
	if got := modes[trustHelperAsset]; got != 0o755 {
		t.Errorf("tar entry %q mode = %o, want 755 so the installed command is executable", trustHelperAsset, got)
	}
	if got := modes[pathMergeAsset]; got != 0o644 {
		t.Errorf("tar entry %q mode = %o, want 644 (only the helper is a command)", pathMergeAsset, got)
	}
}

// TestWriteDockerfileTarShipsTrustHelperLicense covers the license asset in the
// synthetic build context. The helper is upstream's Apache-2.0 work, so whoever
// receives it — a built runner image, or the release binary that embeds the same
// bytes — must also receive the license copy section 4(a) requires. The final
// stage COPYs the context entry, so it travels as a non-executable regular file.
func TestWriteDockerfileTarShipsTrustHelperLicense(t *testing.T) {
	var context bytes.Buffer
	if err := writeDockerfileTar(&context, []byte("FROM scratch\n")); err != nil {
		t.Fatalf("writeDockerfileTar: %v", err)
	}

	entries, modes := readTarEntries(t, &context)

	want, err := os.ReadFile(vendoredTrustHelperLicensePath)
	if err != nil {
		t.Fatalf("read vendored trust helper license: %v", err)
	}
	if got := entries[trustHelperLicenseAsset]; got != string(want) {
		t.Errorf(
			"tar entry %q carries %d bytes, want the vendored license's %d",
			trustHelperLicenseAsset,
			len(got),
			len(want),
		)
	}
	if got := modes[trustHelperLicenseAsset]; got != 0o644 {
		t.Errorf("tar entry %q mode = %o, want 644 (the license is not a command)", trustHelperLicenseAsset, got)
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

func TestUserBuildArgsCarryIdentityComponents(t *testing.T) {
	id := imageIdentity{
		ProjectDockerfileHash: "hash",
		AgentVersion:          "1.2.3",
		Docker:                "false",
		User:                  "1000:1000",
	}
	args := userBuildArgs(
		1000, 1000, agentOpencode(t).ImageSpec(),
		testBuildRequest(nil, "1.2.3", "base", "id", id),
	)
	for key, want := range map[string]string{
		projectDockerfileHashArg: "hash",
		agentVersionArg:          "1.2.3",
		dockerArg:                "false",
		userArg:                  "1000:1000",
	} {
		if args[key] == nil || *args[key] != want {
			t.Errorf("build arg %s = %v, want %q", key, args[key], want)
		}
	}
}

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

func TestImageRefCandidates(t *testing.T) {
	const slug = "proj-1mjusbm3wikhb0"
	contentID := "9f2c1a7b4d6e0f31"
	contentRef := naming.ContentImageRef(slug, "opencode", contentID)
	dockerTag := naming.DockerImageRef(slug, "opencode")
	nameRef := naming.RunnerImageRef(slug, "opencode")
	identity := naming.ImageIdentity{Slug: slug, Agent: "opencode", ContentID: contentID}

	cases := []struct {
		name string
		info ImageInfo
		want []string
	}{
		{
			"content, name, and legacy refs in preference order",
			ImageInfo{Ref: contentRef, Identity: identity, DockerTag: dockerTag},
			[]string{contentRef, nameRef, dockerTag},
		},
		{
			"the identity supplies the name ref without a Docker tag",
			ImageInfo{Ref: contentRef, Identity: identity},
			[]string{contentRef, nameRef},
		},
		{
			"empty ref is dropped",
			ImageInfo{Ref: "", Identity: identity, DockerTag: dockerTag},
			[]string{nameRef, dockerTag},
		},
		{
			"duplicate refs are deduplicated",
			ImageInfo{Ref: nameRef, Identity: identity, DockerTag: nameRef},
			[]string{nameRef},
		},
		{
			"a foreign docker tag alone contributes no name ref",
			ImageInfo{Ref: "", DockerTag: "docker.io/library/foreign:1"},
			[]string{"docker.io/library/foreign:1"},
		},
	}
	for _, tc := range cases {
		if got := imageRefCandidates(tc.info); !slices.Equal(got, tc.want) {
			t.Errorf("%s: imageRefCandidates() = %v, want %v", tc.name, got, tc.want)
		}
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

func TestEnsureLoadedVerboseReportsUpToDate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:same", Config: dockerConfigWith("", nil)}, nil
		},
	})
	ui := &termio.Mock{}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}
	if _, err := EnsureLoaded(
		context.Background(),
		docker.Get(),
		msbClient,
		ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:same"},
		ui,
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "is up to date") {
		t.Errorf("verbose calls = %v, want an up-to-date line", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsNotPresent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	ui := &termio.Mock{}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: "agents-sandbox/runner-p:opencode", Ref: "agents-sandbox/runner-p:opencode"}, ui,
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "not present in microsandbox cache") {
		t.Errorf("verbose calls = %v, want a not-present reason", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsStaleCache(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:docker-new", Config: dockerConfigWith("", nil)}, nil
		},
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	ui := &termio.Mock{}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:msb-old"}, nil
		},
	}
	if _, err := EnsureLoaded(
		context.Background(),
		docker.Get(),
		msbClient,
		ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:docker-new"},
		ui,
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "cached content differs from the Docker image") {
		t.Errorf("verbose calls = %v, want a stale-cache reason", ui.VerboseCalls)
	}
}

func TestEnsureLoadedReturnsErrorWhenLoadFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageLoadFn: func(_ context.Context, _ string, _ io.Reader) error {
			return errors.New("load boom")
		},
	}
	info := ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:new"}
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient, info, &termio.Mock{}); err == nil {
		t.Fatal("expected EnsureLoaded to return an error when ImageLoad fails")
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
			if _, err := EnsureLoaded(
				context.Background(),
				docker.Get(),
				msbClient,
				tc.info,
				&termio.Mock{},
			); err != nil {
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
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient, info, ui); err != nil {
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
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient, info, &termio.Mock{}); err != nil {
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
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient, info, &termio.Mock{}); err != nil {
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
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
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
	_, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
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

// TestEnsureLoadedReusesContentRef pins the primary reuse path: when the cache
// already holds the content under the content reference, nothing is reimported.
func TestEnsureLoadedReusesContentRef(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == contentRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the content ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != contentRef {
		t.Errorf("info.Ref = %q, want the reused content ref %q", info.Ref, contentRef)
	}
}

// TestEnsureLoadedReusesNameRefForMigration pins that a 0.6.0 install, which
// cached the image under the prefix-free name reference, keeps its cache: the
// name ref is derived from the image's structured identity and reused when its
// content matches.
func TestEnsureLoadedReusesNameRefForMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	nameRef := naming.RunnerImageRef("test-project", "opencode")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == nameRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the name ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != nameRef {
		t.Errorf("info.Ref = %q, want the reused name ref %q", info.Ref, nameRef)
	}
}

// TestEnsureLoadedReusesNameRefFromIdentity pins that the name-ref candidate
// comes from the image's structured identity rather than from parsing the
// Docker tag: an info that names its (slug, agent, contentID) reuses the
// cached name reference even when no Docker tag accompanies it.
func TestEnsureLoadedReusesNameRefFromIdentity(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	nameRef := naming.RunnerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == nameRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the identity's name ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != nameRef {
		t.Errorf("info.Ref = %q, want the reused name ref %q", info.Ref, nameRef)
	}
}

// TestEnsureLoadedReusesLegacyPrefixedRefForMigration pins that a pre-0.6
// install, which cached the image under the prefixed Docker tag, keeps its
// cache: the Docker-tag candidate is reused when its content matches.
func TestEnsureLoadedReusesLegacyPrefixedRefForMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == dockerTag {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the prefixed ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != dockerTag {
		t.Errorf("info.Ref = %q, want the reused prefixed ref %q", info.Ref, dockerTag)
	}
}

// TestEnsureLoadedLoadsUnderContentRefWhenNoCandidateMatches pins the fresh-load
// path: with no candidate cached, the Docker image is loaded under the content
// reference.
func TestEnsureLoadedLoadsUnderContentRefWhenNoCandidateMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	var savedRefs []string
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, refs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			savedRefs = refs
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	ui := &termio.Mock{}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:new"},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(savedRefs) != 1 || savedRefs[0] != dockerTag {
		t.Errorf("ImageSave refs = %v, want the Docker tag %q", savedRefs, dockerTag)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != contentRef {
		t.Errorf("LoadedImages = %v, want the content ref %q", msbClient.LoadedImages, contentRef)
	}
	if info.Ref != contentRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, contentRef)
	}
}

// TestEnsureLoadedLoadsUnderContentRefWhenContentDiffers pins that a cached
// candidate holding stale content is not reused: the image loads under the
// content ref, and the load reason reports the content mismatch.
func TestEnsureLoadedLoadsUnderContentRefWhenContentDiffers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == dockerTag {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:old"}, nil
		},
	}
	ui := &termio.Mock{}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:new"},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != contentRef {
		t.Errorf("LoadedImages = %v, want the content ref %q", msbClient.LoadedImages, contentRef)
	}
	if info.Ref != contentRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, contentRef)
	}
}

// TestEnsureLoadedFallsBackToDockerTagWhenRefEmpty pins the legacy-only
// resolution: an ImageInfo built before the ref was resolved carries only the
// Docker tag, so loading falls back to it as the microsandbox reference.
func TestEnsureLoadedFallsBackToDockerTagWhenRefEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: "", Digest: "sha256:new"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if info.Ref != legacyRef {
		t.Errorf("info.Ref = %q, want the Docker tag %q", info.Ref, legacyRef)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != legacyRef {
		t.Errorf("LoadedImages = %v, want [%s]", msbClient.LoadedImages, legacyRef)
	}
}

// TestEnsureLoadedReusesLegacyRefWhenContentMatches pins the no-reimport rule:
// when a pre-change install already cached the image under the legacy reference
// with the same content, that reference is reused instead of loading a second
// copy under the prefix-free reference.
func TestEnsureLoadedReusesLegacyRefWhenContentMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the legacy ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != legacyRef {
		t.Errorf("info.Ref = %q, want the reused legacy ref %q", info.Ref, legacyRef)
	}
}

// TestEnsureLoadedLoadsUnderNewRefWhenLegacyAbsent pins that a fresh install
// loads the image under the prefix-free reference.
func TestEnsureLoadedLoadsUnderNewRefWhenLegacyAbsent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:new"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != newRef {
		t.Errorf("LoadedImages = %v, want [%s]", msbClient.LoadedImages, newRef)
	}
	if info.Ref != newRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, newRef)
	}
}

// TestEnsureLoadedLoadsUnderNewRefWhenLegacyContentDiffers pins that a legacy
// reference holding stale content is not reused: the current Docker image is
// loaded under the prefix-free reference instead.
func TestEnsureLoadedLoadsUnderNewRefWhenLegacyContentDiffers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:old"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:new"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != newRef {
		t.Errorf("LoadedImages = %v, want [%s]", msbClient.LoadedImages, newRef)
	}
	if info.Ref != newRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, newRef)
	}
}

func TestCachedImageMatchesMsbInspectError(t *testing.T) {
	msbClient := &msb.MockMsbClient{
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("msb inspect boom")
		},
	}
	if cachedImageMatches(
		context.Background(),
		msbClient,
		"ref",
		ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:x"},
	) {
		t.Error("expected false when the microsandbox inspect fails")
	}
}

// TestCachedImageMatches pins the content-identity contract: only a cached
// digest equal to the Docker image ID counts as a match. A cached image that
// records no digest is unknown content and must reload — the dockerfile-id
// label is a source signature that can match while the bytes differ, so it is
// never trusted.
func TestCachedImageMatches(t *testing.T) {
	cases := []struct {
		name      string
		msbDigest string
		msbLabels map[string]string
		msbNil    bool
		info      ImageInfo
		want      bool
	}{
		{
			name:      "equal digest matches",
			msbDigest: "sha256:same",
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:same"},
			want:      true,
		},
		{
			name:      "different digest never matches, even with equal label",
			msbDigest: "sha256:old",
			msbLabels: map[string]string{dockerfileIDLabelKey: "abc"},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			want:      false,
		},
		{
			name:      "missing digest never matches, even with equal label",
			msbLabels: map[string]string{dockerfileIDLabelKey: "abc123"},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc123"},
			want:      false,
		},
		{
			name:      "missing digest and no labels never matches",
			msbLabels: map[string]string{},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			want:      false,
		},
		{
			name:      "cached digest with no requested digest never matches",
			msbDigest: "sha256:same",
			info:      ImageInfo{DockerTag: "ref", Ref: "ref"},
			want:      false,
		},
		{
			name: "no cached digest and no requested digest never matches",
			info: ImageInfo{DockerTag: "ref", Ref: "ref"},
			want: false,
		},
		{
			name:   "nil inspect result never matches",
			msbNil: true,
			info:   ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msbClient := &msb.MockMsbClient{
				ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
					if tc.msbNil {
						return nil, nil //nolint:nilnil // the seam can return a nil config with no error
					}
					return &msbSdk.ImageConfig{Digest: tc.msbDigest, Labels: tc.msbLabels}, nil
				},
			}
			if got := cachedImageMatches(context.Background(), msbClient, tc.info.Ref, tc.info); got != tc.want {
				t.Errorf("cachedImageMatches() = %v, want %v", got, tc.want)
			}
		})
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

func TestEnsureImageSuccess(t *testing.T) {
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
	})

	info, err := EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		a,
		"test-project",
		nil,
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Env["PATH"] != "/usr/bin" {
		t.Errorf("info.Env = %v", info.Env)
	}
}

func TestEnsureImageReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDefaultErrorDockerMock(t)

	_, err := EnsureImage(context.Background(), docker.Get(), configpaths.Get(), a,
		"test-project",
		nil,
		BuildOptions{Force: true},
		&termio.Mock{},
	)
	if err == nil {
		t.Error("expected EnsureImage to return an error when Docker build fails")
	}
}

func TestEnsureImageReturnsErrorWhenVersionResolveFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "", errors.New("resolve boom")
	})
	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a, []byte("FROM x\n"), "test-project",
		BuildOptions{}, &termio.Mock{},
	)
	if err == nil {
		t.Error("expected error when resolving agent version fails")
	}
}

func TestEnsureImageReturnsErrorWhenDeferredVersionResolveFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		if requested != "" {
			t.Errorf("resolver requested = %q, want empty version", requested)
		}
		return "", errors.New("resolve boom")
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
	})
	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a, []byte("FROM x\n"), "test-project",
		BuildOptions{UserProvided: true}, &termio.Mock{},
	)
	if err == nil {
		t.Error("expected error when resolving the deferred agent version fails")
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

func TestEnsureImageCannotInspectBuiltImage(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if !built {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{}, errors.New("inspect boom")
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			built = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a, []byte("FROM debian:trixie-slim\n"),
		"proj", BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "cannot inspect built image") {
		t.Errorf("EnsureImageWithClient error = %v, want an inspect-built-image failure", err)
	}
}

func TestEnsureImageCannotReadImageInfo(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	calls := 0
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			calls++
			switch calls {
			case 1:
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			case 2:
				return client.ImageInspectResult{
					ID: "sha256:built", Config: dockerConfigWith("", nil),
				}, nil
			default:
				return client.ImageInspectResult{}, errors.New("read env boom")
			}
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a, []byte("FROM debian:trixie-slim\n"),
		"proj", BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "inspect built image") {
		t.Errorf("EnsureImageWithClient error = %v, want an inspect-built-image failure reading env", err)
	}
}

func TestEnsureImageReturnsBuildError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, errors.New("build boom")
		},
	})
	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a, []byte("FROM debian:trixie-slim\n"),
		"proj", BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected EnsureImageWithClient to return an error when the build fails")
	}
}

func TestEnsureImageReturnsErrorWhenBuildFails(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	l := &termio.Mock{}
	docker.WithDefaultErrorDockerMock(t)
	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		nil,
		"test-project",
		BuildOptions{Force: true},
		l,
	)
	if err == nil {
		t.Error("expected error when Docker build fails")
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

func TestBuildSuccess(t *testing.T) {
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

func TestBuildReturnsErrorWhenImageBuildFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDefaultErrorDockerMock(t)
	if _, err := Build(context.Background(), docker.Get(), &msb.MockMsbClient{}, configpaths.Get(),
		a,
		"test-project",
		nil,
		BuildOptions{Force: true},
		&termio.Mock{},
	); err == nil {
		t.Error("expected Build to return an error when image build fails")
	}
}

func TestBuildReturnsErrorWhenLoadFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     "sha256:abc123",
				Config: dockerConfigWith("1.2.3", nil),
			}, nil
		},
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, errors.New("docker save failed")
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
	); err == nil {
		t.Error("expected Build to return an error when loading the image into microsandbox fails")
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

func TestBaseImageRef(t *testing.T) {
	a := agentOpencode(t)
	if got := baseImageRef(renderBytes(t, a, nil, dockerOff)); got != distroBaseRef {
		t.Errorf("default baseImageRef = %q, want %q", got, distroBaseRef)
	}
	custom := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, custom, dockerOff)); got != "ubuntu:24.04" {
		t.Errorf("custom baseImageRef = %q", got)
	}
}

func TestBaseImageRefFallsBackToRawToken(t *testing.T) {
	if got := baseImageRef([]byte("FROM INVALIDREF\n")); got != "INVALIDREF" {
		t.Errorf("baseImageRef = %q, want the raw unparseable token", got)
	}
}

// TestBaseImageRefComposition pins that baseImageRef resolves the final stage's
// FROM through any stage aliases and returns it unchanged: the tool-owned base
// (qualified where it is rendered) and a user's own base alike. Re-qualifying a
// user's base would redirect the lookup to Docker Hub even when the engine
// resolves that name through a mirror, a search registry, or a rewrite. The
// label's familiar spelling is applied by resolveBaseDigest, covered separately.
func TestBaseImageRefComposition(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if got := baseImageRef(renderBytes(t, a, nil, dockerOff)); got != distroBaseRef {
		t.Errorf("managed baseImageRef = %q, want %q", got, distroBaseRef)
	}
	custom := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, custom, dockerOff)); got != "ubuntu:24.04" {
		t.Errorf("custom baseImageRef = %q, want ubuntu:24.04", got)
	}
	aliased := []byte("FROM fedora:latest AS base\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, aliased, dockerOff)); got != "fedora:latest" {
		t.Errorf("aliased baseImageRef = %q, want fedora:latest", got)
	}
	private := []byte("FROM company/base:latest\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, private, dockerOff)); got != "company/base:latest" {
		t.Errorf("private baseImageRef = %q, want the token the engine resolves, company/base:latest", got)
	}
}

func TestFinalStageTokenSkipsFromWithoutImage(t *testing.T) {
	if got := finalStageToken([]byte("FROM --platform=linux/amd64\n")); got != "" {
		t.Errorf("finalStageToken = %q, want empty", got)
	}
}

func TestFinalStageTokenResolvesAlias(t *testing.T) {
	dockerfile := []byte("FROM golang:1.24 AS build\nFROM build AS final\nRUN echo hi\n")
	if got := finalStageToken(dockerfile); got != "golang:1.24" {
		t.Errorf("finalStageToken = %q, want golang:1.24 resolved through stage aliases", got)
	}
}

// TestResolveBaseDigestRefreshPullsPresentBase pins that --rebuild refreshes the
// base from its registry even when a copy is cached locally.
func TestResolveBaseDigestRefreshPullsPresentBase(t *testing.T) {
	pulled := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if pulled {
				return client.ImageInspectResult{ID: "sha256:fresh"}, nil
			}
			return client.ImageInspectResult{ID: "sha256:stale"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = true
			return docker.PullResponse{Body: strings.NewReader("")}, nil
		},
	})

	ui := &termio.Mock{}

	got, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", true, ui)
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != "debian:trixie-slim@sha256:fresh" {
		t.Errorf("resolveBaseDigest = %q, want the freshly pulled digest", got)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none after a successful refresh", ui.WarnCalls)
	}
}

// TestResolveBaseDigestRefreshFallsBackToLocalBase pins that a failed refresh
// (offline, registry down) keeps building from the cached base with a warning.
func TestResolveBaseDigestRefreshFallsBackToLocalBase(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:cached"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, errors.New("dial tcp: no route to host")
		},
	})
	ui := &termio.Mock{}

	got, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", true, ui)
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != "debian:trixie-slim@sha256:cached" {
		t.Errorf("resolveBaseDigest = %q, want the cached digest", got)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "no route to host") {
		t.Errorf("warnings = %v, want one naming the refresh failure", ui.WarnCalls)
	}
}

func TestResolveBaseDigestRefreshFailsWithoutLocalBase(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("No such image")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, errors.New("pull access denied")
		},
	})

	_, err := resolveBaseDigest(context.Background(), docker.Get(), "private/base:latest", true, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "pull base image") {
		t.Errorf("resolveBaseDigest error = %v, want the pull failure", err)
	}
}

func TestResolveBaseDigestUsesLocalBaseWithoutPulling(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:cached"}, nil
		},
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			t.Errorf("unexpected pull of %s for a cached base without refresh", ref)
			return nil, errors.New("unexpected pull")
		},
	})

	got, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", false, &termio.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != "debian:trixie-slim@sha256:cached" {
		t.Errorf("resolveBaseDigest = %q, want the cached digest", got)
	}
}

func TestResolveBaseDigestLogsPullOfAbsentBase(t *testing.T) {
	pulled := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if !pulled {
				return client.ImageInspectResult{}, errors.New("No such image")
			}
			return client.ImageInspectResult{ID: "sha256:pulled"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = true
			return docker.PullResponse{Body: strings.NewReader("")}, nil
		},
	})
	ui := &termio.Mock{}

	if _, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", false, ui); err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if len(ui.VerboseCalls) != 1 || !strings.Contains(ui.VerboseCalls[0], "not present locally, pulling") {
		t.Errorf("verbose lines = %v, want the pull notice", ui.VerboseCalls)
	}
}

func TestResolveBaseDigestWrapsCauses(t *testing.T) {
	cause := errors.New("cause")
	for _, tc := range []struct {
		name    string
		pullErr error
		waitErr error
	}{
		{"pull call fails", cause, nil},
		{"pull stream fails", nil, cause},
		{"inspect after pull fails", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, cause
				},
				ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
					if tc.pullErr != nil {
						return nil, tc.pullErr
					}
					return docker.PullResponse{Body: strings.NewReader(""), WaitErr: tc.waitErr}, nil
				},
			})
			_, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", false, &termio.Mock{})
			if !errors.Is(err, cause) {
				t.Errorf("resolveBaseDigest error = %v, want it to wrap the cause", err)
			}
		})
	}
}

// TestResolveBaseDigestPullsAbsentBase verifies the pull-then-inspect path: the
// reference is pulled as given (the caller passes the qualified base ref), while
// the returned provenance label keeps the familiar spelling.
func TestResolveBaseDigestPullsAbsentBase(t *testing.T) {
	var pulled string
	inspects := 0
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			inspects++
			if inspects == 1 {
				return client.ImageInspectResult{}, errors.New("not found")
			}
			return client.ImageInspectResult{ID: "sha256:pulled"}, nil
		},
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = ref
			return docker.PullResponse{Body: strings.NewReader("")}, nil
		},
	})
	const qualified = "docker.io/library/debian:trixie-slim"
	got, err := resolveBaseDigest(context.Background(), docker.Get(), qualified, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if pulled != qualified {
		t.Errorf("ImagePull called with %q, want %q", pulled, qualified)
	}
	if got != "debian:trixie-slim@sha256:pulled" {
		t.Errorf("resolveBaseDigest = %q, want the familiar spelling in the label", got)
	}
}

// TestResolveBaseDigestSurfacesPullFailure pins that a registry-level pull
// failure is surfaced as the classified base-image error with its troubleshooting
// link, not swallowed and re-reported as a misleading post-pull inspect failure.
// Both delivery paths are covered: the pull call's own error and a registry
// failure inside the stream. The test also asserts a surfaced failure leaves no
// second inspect to confuse the message.
func TestResolveBaseDigestSurfacesPullFailure(t *testing.T) {
	const pullFailure = `pull access denied for private/base, repository does not exist or may require 'docker login'`
	for _, tc := range []struct {
		name string
		pull func() (client.ImagePullResponse, error)
	}{
		{
			name: "failure delivered inside the stream",
			pull: func() (client.ImagePullResponse, error) {
				return docker.PullResponse{WaitErr: errors.New(pullFailure)}, nil
			},
		},
		{
			name: "failure returned by the pull call",
			pull: func() (client.ImagePullResponse, error) {
				return nil, errors.New(pullFailure)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspects := 0
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					inspects++
					return client.ImageInspectResult{}, errors.New("No such image")
				},
				ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
					return tc.pull()
				},
			})
			_, err := resolveBaseDigest(
				context.Background(),
				docker.Get(),
				"private/base:latest",
				false,
				&termio.Mock{},
			)
			if err == nil || !strings.Contains(err.Error(), "base image not found or not logged in") {
				t.Errorf("resolveBaseDigest error = %v, want the classified base-image failure", err)
			}
			if err != nil && !strings.Contains(err.Error(), "the-base-image-cannot-be-found") {
				t.Errorf("resolveBaseDigest error = %v, want the troubleshooting docs link", err)
			}
			if inspects != 1 {
				t.Errorf("ImageInspect called %d times, want 1 (a surfaced pull failure must not re-inspect)", inspects)
			}
		})
	}
}

// TestResolveBaseDigestKeepsUnparseableRef covers a project Dockerfile FROM that
// is not a valid image reference: the recorded label carries it through
// unchanged rather than dropping the base it could not parse.
func TestResolveBaseDigestKeepsUnparseableRef(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:odd"}, nil
		},
	})
	const unparseable = "Not A Valid Ref"
	got, err := resolveBaseDigest(context.Background(), docker.Get(), unparseable, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != unparseable+"@sha256:odd" {
		t.Errorf("resolveBaseDigest = %q, want the ref carried through unchanged", got)
	}
}

func TestResolveBaseDigestPullError(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("not found")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, errors.New("pull boom")
		},
	})
	_, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", false, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "pull base image") {
		t.Errorf("resolveBaseDigest error = %v, want a pull failure", err)
	}
}

func TestResolveBaseDigestInspectAfterPullError(t *testing.T) {
	inspects := 0
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			inspects++
			return client.ImageInspectResult{}, errors.New("not found")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return docker.PullResponse{Body: strings.NewReader("")}, nil
		},
	})
	_, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", false, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "inspect pulled base image") {
		t.Errorf("resolveBaseDigest error = %v, want an inspect-pulled-image failure", err)
	}
}

func TestResolveBaseDigestPullStreamError(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("not found")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return docker.PullResponse{WaitErr: errors.New("read boom")}, nil
		},
	})
	_, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", false, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "pull base image") {
		t.Errorf("resolveBaseDigest error = %v, want a pull stream failure", err)
	}
}

func TestLocalBaseImageTreatsUnknownImageAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result client.ImageInspectResult
		err    error
		want   string
	}{
		{"present", client.ImageInspectResult{ID: "sha256:cached"}, nil, "debian:trixie-slim@sha256:cached"},
		{"inspect error", client.ImageInspectResult{ID: "sha256:cached"}, errors.New("boom"), ""},
		{"empty image id", client.ImageInspectResult{}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					return tc.result, tc.err
				},
			})
			if got := localBaseImage(context.Background(), docker.Get(), "debian:trixie-slim"); got != tc.want {
				t.Errorf("localBaseImage = %q, want %q", got, tc.want)
			}
		})
	}
}

func verboseContains(ui *termio.Mock, substr string) bool {
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, substr) {
			return true
		}
	}
	return false
}

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

// testDockerImageID is a full sha256 image ID whose 16-char prefix keys the
// content-addressed reference in tests.
const testDockerImageID = "sha256:9f2c1a7b4d6e0f3199999999999999999999999999999999999999999999999999"

// testContentIdentity is the structured identity of the image the reuse tests
// describe: project "test-project", agent opencode, content id 9f2c1a7b4d6e0f31.
func testContentIdentity() naming.ImageIdentity {
	return naming.ImageIdentity{Slug: "test-project", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31"}
}

// readTarEntries returns the tar's entry contents and modes keyed by name.
func readTarEntries(t *testing.T, r io.Reader) (map[string]string, map[string]int64) {
	t.Helper()
	entries := map[string]string{}
	modes := map[string]int64{}
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries, modes
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry %q: %v", header.Name, err)
		}
		entries[header.Name] = string(content)
		modes[header.Name] = header.Mode
	}
}

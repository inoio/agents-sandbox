package image

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

func TestBuildDockerImageReturnsBuildFailure(t *testing.T) {
	a := agentOpencode(t)
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, errors.New("build boom")
		},
	})
	err := buildDockerImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
		"tag", "label", false, &termiotest.Mock{},
	)
	if err == nil {
		t.Fatal("expected buildDockerImage to return an error when the build fails")
	}
}

func TestBuildDockerImageForwardsBuildOutputToVerbose(t *testing.T) {
	a := agentOpencode(t)
	ui := &termiotest.Mock{}
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
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
	m := &dockertest.MockDockerClient{}
	var capturedBuildArgs map[string]*string
	m.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		capturedBuildArgs = opts.BuildArgs
		return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	dockertest.WithDockerMock(t, m)

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

// TestBuildImageClassifiesBuildFailures pins the error a failed build reports.
// A missing or private base gets the login hint. An image name with no registry,
// which podman refuses to resolve without search registries, points at the
// troubleshooting section naming that fix whether the engine streams the error
// or returns it from the build call. Anything else wraps the engine's message
// without a hint.
func TestBuildImageClassifiesBuildFailures(t *testing.T) {
	a := agentOpencode(t)
	const unresolvedName = "short-name debian:trixie-slim did not resolve to an alias and no " +
		"unqualified search registries are defined in /etc/containers/registries.conf"
	streamed := func(message string) func() (client.ImageBuildResult, error) {
		return func() (client.ImageBuildResult, error) {
			return client.ImageBuildResult{
				Body: io.NopCloser(strings.NewReader(`{"errorDetail":{"message":"` + message + `"}}`)),
			}, nil
		}
	}
	returned := func(message string) func() (client.ImageBuildResult, error) {
		return func() (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, errors.New(message)
		}
	}
	for _, tc := range []struct {
		name       string
		build      func() (client.ImageBuildResult, error)
		want       []string
		wantAbsent string
	}{
		{
			name:  "build call error",
			build: returned("image build failed"),
			want:  []string{"docker image build failed", "image build failed"},
		},
		{
			name:  "pull access denied",
			build: streamed("pull access denied for base"),
			want:  []string{"base image not found or not logged in"},
		},
		{
			name:  "registry unauthorized",
			build: streamed("unauthorized: The client does not have permission for manifest"),
			want: []string{
				"base image not found or not logged in",
				"troubleshooting.html#the-base-image-cannot-be-found",
			},
		},
		{
			name:  "registry denied",
			build: returned("denied: requested access to the resource is denied"),
			want: []string{
				"base image not found or not logged in",
				"troubleshooting.html#the-base-image-cannot-be-found",
			},
		},
		{
			name:  "no basic auth credentials",
			build: returned(`Error response from daemon: Get "https://private.example.com/v2/": no basic auth credentials`),
			want: []string{
				"base image not found or not logged in",
				"troubleshooting.html#the-base-image-cannot-be-found",
			},
		},
		{
			name:       "local permission denied",
			build:      returned("permission denied while trying to connect to the docker API"),
			want:       []string{"docker image build failed"},
			wantAbsent: "not found or not logged in",
		},
		{
			name:       "other streamed error",
			build:      streamed("some other failure"),
			want:       []string{"some other failure"},
			wantAbsent: "not found or not logged in",
		},
		{
			name:  "unresolved name streamed",
			build: streamed(unresolvedName),
			want:  []string{"troubleshooting.html#the-base-image-cannot-be-found"},
		},
		{
			name:  "unresolved name returned",
			build: returned(unresolvedName),
			want:  []string{"troubleshooting.html#the-base-image-cannot-be-found"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
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
			if err == nil {
				t.Fatal("buildImage error = nil, want a build failure")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("buildImage error = %v, want it to contain %q", err, want)
				}
			}
			if tc.wantAbsent != "" && strings.Contains(err.Error(), tc.wantAbsent) {
				t.Errorf("buildImage error = %v, must not contain %q", err, tc.wantAbsent)
			}
		})
	}
}

// TestBuildImageSurfacesTheBuilderSelectionFailure pins that a ping failure
// during builder selection is returned to the caller, so a build on an
// unreachable engine never silently assumes a builder.
func TestBuildImageSurfacesTheBuilderSelectionFailure(t *testing.T) {
	wantErr := errors.New("ping boom")
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		PingFn: func(context.Context, client.PingOptions) (client.PingResult, error) {
			return client.PingResult{}, wantErr
		},
	})

	err := buildImage(
		context.Background(), docker.Get(), agentOpencode(t),
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "", "debian:trixie-slim", "", imageIdentity{}),
		"tag", false, func(string) {},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("buildImage error = %v, want %v", err, wantErr)
	}
}

// TestBuildImageSendsTheRenderedDockerfileAsBuildContext pins that the engine
// receives a tar build context whose Dockerfile is the rendered one.
func TestBuildImageSendsTheRenderedDockerfileAsBuildContext(t *testing.T) {
	rendered := []byte("FROM debian:trixie-slim\nRUN echo context\n")
	var dockerfile []byte
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageBuildFn: func(_ context.Context, buildContext io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			tr := tar.NewReader(buildContext)
			for header, err := tr.Next(); err == nil; header, err = tr.Next() {
				if header.Name == "Dockerfile" {
					dockerfile, _ = io.ReadAll(tr)
				}
			}
			return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
		},
	})
	if err := buildImage(
		context.Background(), docker.Get(), agentOpencode(t),
		testBuildRequest(rendered, "", "debian:trixie-slim", "", imageIdentity{}),
		"tag", false, func(string) {},
	); err != nil {
		t.Fatalf("buildImage: %v", err)
	}
	if !bytes.Equal(dockerfile, rendered) {
		t.Errorf("build context Dockerfile = %q, want the rendered %q", dockerfile, rendered)
	}
}

func TestBuildImageNoCacheFollowsParameter(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	a, _ := agent.Lookup("opencode")

	for _, noCache := range []bool{true, false} {
		t.Run(strconv.FormatBool(noCache), func(t *testing.T) {
			m := &dockertest.MockDockerClient{}
			var capturedNoCache bool
			m.ImageBuildFn = func(
				_ context.Context, _ io.Reader, opts client.ImageBuildOptions,
			) (client.ImageBuildResult, error) {
				capturedNoCache = opts.NoCache
				return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}
			dockertest.WithDockerMock(t, m)

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
	configpathstest.WithMockConfigPaths(t)
	orig := defaultAgentVersionResolver
	defaultAgentVersionResolver = func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		return requested, nil
	}
	t.Cleanup(func() { defaultAgentVersionResolver = orig })

	m := &dockertest.MockDockerClient{}
	var captured map[string]*string
	m.ImageInspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{ID: "sha256:base"}, nil
	}
	m.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		captured = opts.BuildArgs
		return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	dockertest.WithDockerMock(t, m)

	_, err := EnsureImageWithClient(context.Background(), docker.Get(), a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: true, AgentVersion: "1.2.3"},
		&termiotest.Mock{},
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

func TestScanBuildOutputRejectsNonJSONOutput(t *testing.T) {
	err := scanBuildOutput(strings.NewReader("{not json"), func(string) {}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "unexpected Docker build output") {
		t.Errorf("scanBuildOutput error = %v, want an unexpected-output error", err)
	}
}

// TestScanBuildOutputRecordsTheRawChunk pins that the recorder sees a chunk's
// raw bytes, so a line split across two stream messages can be rejoined.
func TestScanBuildOutputRecordsTheRawChunk(t *testing.T) {
	body := strings.NewReader(`{"stream":"Temporary fail"}` + "\n" + `{"stream":"ure resolving\n"}`)

	var raw []string
	if err := scanBuildOutput(body, func(string) {}, func(s string) { raw = append(raw, s) }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"Temporary fail", "ure resolving\n"}; !reflect.DeepEqual(raw, want) {
		t.Errorf("recorded chunks = %q, want the raw chunks %q", raw, want)
	}
}

func TestScanBuildOutputForwardsStreamLines(t *testing.T) {
	body := strings.NewReader(`{"stream":"Step 1/1 : FROM debian\n"}
{"stream":"\n"}
{"stream":"Successfully built abc123\n"}`)

	var got []string
	if err := scanBuildOutput(body, func(s string) { got = append(got, s) }, func(string) {}); err != nil {
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

	err := scanBuildOutput(body, func(string) {}, func(string) {})
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

func TestMustWriteBuildContextPanicsOnAWriteError(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "disk full") {
			t.Errorf("recovered %v, want a panic naming the write error", recovered)
		}
	}()
	mustWriteBuildContext(errors.New("disk full"))
}

// TestBuildDockerImageRefusesAfterAnUnapprovedRead pins the build-side stop: a
// project input read as changed since its approval keeps anything from being
// built from this run's inputs.
func TestBuildDockerImageRefusesAfterAnUnapprovedRead(t *testing.T) {
	a := agentOpencode(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	t.Cleanup(projectguard.Install([]string{dir}, map[string]projectguard.Approval{
		path: projectguard.ApprovalOf([]byte("FROM approved\n")),
	}))
	if err := os.WriteFile(path, []byte("FROM changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := projectguard.ReadFile(path, os.ReadFile); err == nil {
		t.Fatal("the changed Dockerfile read as approved")
	}
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageBuildFn: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
			t.Fatal("built an image after an unapproved read")
			return client.ImageBuildResult{}, nil
		},
	})
	err := buildDockerImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
		"tag", "label", false, &termiotest.Mock{},
	)
	if !errors.Is(err, projectguard.ErrNotApproved) {
		t.Errorf("buildDockerImage = %v, want ErrNotApproved", err)
	}
}

func TestBuildOutputTailKeepsTheLastLines(t *testing.T) {
	var tail buildOutputTail
	for i := range 25 {
		tail.add(fmt.Sprintf("line %d\n", i))
	}
	// The cap is 20, so 25 recorded lines leave line 5 through line 24. The
	// bounds are literals: an expectation written against the constant would move
	// with a mutated cap and never fail.
	var wantLines []string
	for i := 5; i < 25; i++ {
		wantLines = append(wantLines, fmt.Sprintf("line %d", i))
	}
	if got, want := tail.String(), strings.Join(wantLines, "\n"); got != want {
		t.Errorf("String() = %q, want the last 20 lines %q", got, want)
	}
}

func TestBuildOutputTailIgnoresBlankLines(t *testing.T) {
	var tail buildOutputTail
	tail.add("\n")
	tail.add("kept\n\n")
	if got := tail.String(); got != "kept" {
		t.Errorf("String() = %q, want %q", got, "kept")
	}
	if got := (&buildOutputTail{}).String(); got != "" {
		t.Errorf("empty tail String() = %q, want %q", got, "")
	}
}

// TestBuildOutputTailJoinsALineSplitAcrossChunks pins that a chunk boundary in
// the middle of a line does not turn one line into two, so a DNS signal split
// across two log chunks still reaches the classifier and the tail.
func TestBuildOutputTailJoinsALineSplitAcrossChunks(t *testing.T) {
	var tail buildOutputTail
	tail.add("Temporary fail")
	tail.add("ure resolving 'deb.debian.org'\n")
	if got, want := tail.String(), "Temporary failure resolving 'deb.debian.org'"; got != want {
		t.Errorf("String() = %q, want the rejoined line %q", got, want)
	}
}

// TestBuildOutputTailStripsCarriageReturns pins that a CRLF line loses its
// trailing carriage return, so the tail never renders a stray \r.
func TestBuildOutputTailStripsCarriageReturns(t *testing.T) {
	var tail buildOutputTail
	tail.add("line one\r\nline two\r\n")
	if got, want := tail.String(), "line one\nline two"; got != want {
		t.Errorf("String() = %q, want the CR-stripped lines %q", got, want)
	}
}

// TestBuildOutputTailBoundsALineWithoutNewlines pins that a stream that never
// emits a newline (carriage-return progress, a very long line) cannot grow the
// recorder without bound: the unfinished line is capped at 4096 bytes.
func TestBuildOutputTailBoundsALineWithoutNewlines(t *testing.T) {
	var tail buildOutputTail
	for range 100 {
		tail.add(strings.Repeat("x", 1000))
	}
	if got := tail.String(); len(got) != 4096 {
		t.Errorf("tail rendered %d bytes, want the 4096-byte cap", len(got))
	}
}

// TestBuildOutputTailBoundsARecordedLine pins the same cap on a complete line.
func TestBuildOutputTailBoundsARecordedLine(t *testing.T) {
	var tail buildOutputTail
	tail.add(strings.Repeat("y", 10000) + "\n")
	if got := tail.String(); len(got) != 4096 {
		t.Errorf("tail rendered %d bytes, want the 4096-byte cap", len(got))
	}
}

func TestBuildOutputTailKeepsAnUnterminatedFinalLine(t *testing.T) {
	var single buildOutputTail
	single.add("only")
	if got, want := single.String(), "only"; got != want {
		t.Errorf("single-chunk String() = %q, want %q", got, want)
	}

	var tail buildOutputTail
	tail.add("first\nsecond")
	if got, want := tail.String(), "first\nsecond"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestBuildFailureErrorClassifiesNetworkDownloadFailure pins that a failure whose
// error or streamed output names a download/DNS problem gets the troubleshooting
// link and carries the captured output.
func TestBuildFailureErrorClassifiesNetworkDownloadFailure(t *testing.T) {
	err := buildFailureError(
		errors.New("The command '/bin/sh -c apt-get update' returned a non-zero code: 100"),
		"W: Failed to fetch http://deb.debian.org/debian/dists/trixie/InRelease\n"+
			"W: Temporary failure resolving 'deb.debian.org'",
	)
	for _, want := range []string{
		"troubleshooting.html#image-build-fails",
		"Last build output:",
		"Temporary failure resolving 'deb.debian.org'",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

// TestBuildFailureErrorAppendsBuildOutputWithoutANetworkHint pins that a
// non-network failure still reports its output, without the network link.
func TestBuildFailureErrorAppendsBuildOutputWithoutANetworkHint(t *testing.T) {
	err := buildFailureError(errors.New("some other failure"), "E: step exited 2")
	for _, want := range []string{"Last build output:", "E: step exited 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "image-build-fails") {
		t.Errorf("error = %v, must not carry the network hint", err)
	}
}

func TestBuildFailureErrorOmitsEmptyBuildOutput(t *testing.T) {
	err := buildFailureError(errors.New("some other failure"), "")
	if strings.Contains(err.Error(), "Last build output") {
		t.Errorf("error = %v, must not add a block for empty output", err)
	}
}

// TestBuildFailureErrorKeepsTheBaseImageCaseFirst pins that a base-image failure
// wins over a network signal the stream also happens to carry.
func TestBuildFailureErrorKeepsTheBaseImageCaseFirst(t *testing.T) {
	err := buildFailureError(
		errors.New("pull access denied for private-base"),
		"W: Temporary failure resolving 'deb.debian.org'",
	)
	if !strings.Contains(err.Error(), "base image not found or not logged in") {
		t.Errorf("error = %v, want the base-image hint", err)
	}
	if strings.Contains(err.Error(), "image-build-fails") {
		t.Errorf("error = %v, want no network hint for a base-image failure", err)
	}
}

// TestMissingRootlessPrerequisite pins the classifier that lifts the named
// prerequisite out of the injected docker adoption block's stderr line, so a
// failed custom-base build can report the missing package as the leading line
// and drop the duplicate tail block.
func TestMissingRootlessPrerequisite(t *testing.T) {
	for _, tc := range []struct {
		name        string
		buildOutput string
		want        string
		wantFound   bool
	}{
		{
			name: "names the missing package",
			buildOutput: "Step 5/5 : RUN /bin/sh -c set -e; mkdir -p /etc/agents-sandbox; \\\n" +
				"    bin=\"$(command -v dockerd 2>/dev/null || true)\"; \\\n" +
				"    error: rootless docker prerequisite missing: newuidmap",
			want:      "newuidmap",
			wantFound: true,
		},
		{
			name:        "carriage returns around the line",
			buildOutput: "error: rootless docker prerequisite missing: slirp4netns\r\n",
			want:        "slirp4netns",
			wantFound:   true,
		},
		{
			name: "colorized classic stream",
			// The classic builder colorizes a failing step's stderr; the
			// ESC[91m red opener sits before the marker on the same line.
			buildOutput: "\x1b[91merror: rootless docker prerequisite missing: newuidmap\n\x1b[0m",
			want:        "newuidmap",
			wantFound:   true,
		},
		{
			name:        "no marker",
			buildOutput: "E: step exited 2",
			wantFound:   false,
		},
		{
			name:        "tool-only prerequisite is not the rootless marker",
			buildOutput: "error: docker prerequisite missing: iptables",
			wantFound:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := missingRootlessPrerequisite(tc.buildOutput)
			if found != tc.wantFound {
				t.Errorf("missingRootlessPrerequisite() found = %v, want %v", found, tc.wantFound)
			}
			if got != tc.want {
				t.Errorf("missingRootlessPrerequisite() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildFailureErrorNamesRootlessPrerequisiteAndDropsTheTailBlock pins the
// real contract of the prerequisite branch: the missing package leads the
// message, and the duplicate "Last build output" tail block is dropped. The
// engine's own error still echoes the injected command inside the %w wrap; only
// the redundant tail block is removed.
func TestBuildFailureErrorNamesRootlessPrerequisiteAndDropsTheTailBlock(t *testing.T) {
	err := buildFailureError(
		errors.New("The command '/bin/sh -c set -e; mkdir -p /etc/agents-sandbox; \\\n"+
			"    bin=\"$(command -v dockerd 2>/dev/null || true)\"; ...' returned a non-zero code: 1"),
		"Step 5/5 : RUN /bin/sh -c set -e; mkdir -p /etc/agents-sandbox; ...\n"+
			"error: rootless docker prerequisite missing: newuidmap",
	)
	for _, want := range []string{"newuidmap", "rootless docker prerequisite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
	if !strings.HasPrefix(err.Error(), "docker image build failed (the base image is missing") {
		t.Errorf("error = %v, want the missing package to lead the message", err)
	}
	if strings.Contains(err.Error(), "Last build output") {
		t.Errorf("error = %v, want the duplicate tail block dropped when the marker names the prerequisite", err)
	}
}

// TestBuildDockerImageReportsTheMissingRootlessPrerequisite pins the F24
// scenario end to end: a custom base that omits a rootless prerequisite fails
// the build, and the error names the missing package as the leading line,
// without the duplicate tail block, at the default log level. The engine's own
// error still echoes the injected command within the wrapped cause. The cases
// cover the plain stream and the colorized classic stream, where the ESC[91m
// red opener prefixes the marker on the same stderr line.
func TestBuildDockerImageReportsTheMissingRootlessPrerequisite(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "plain stream",
			body: `{"stream":"Step 5/5 : RUN /bin/sh -c set -e; mkdir -p /etc/agents-sandbox; ...\n"}` + "\n" +
				`{"stream":"error: rootless docker prerequisite missing: newuidmap\n"}` + "\n" +
				`{"errorDetail":{"message":"The command '/bin/sh -c set -e; mkdir -p /etc/agents-sandbox; ...' returned a non-zero code: 1"}}`,
		},
		{
			name: "colorized classic stream",
			// The engine JSON-encodes the ESC byte as \u001b; the decoded
			// stream value carries the raw 0x1b before the marker.
			body: `{"stream":"\u001b[91merror: rootless docker prerequisite missing: newuidmap\n\u001b[0m"}` + "\n" +
				`{"errorDetail":{"message":"The command '/bin/sh -c set -e; mkdir -p /etc/agents-sandbox; ...' returned a non-zero code: 1"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := agentOpencode(t)
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
				ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
					return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				},
			})
			err := buildDockerImage(
				context.Background(), docker.Get(), a,
				testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
				"tag", "label", false, &termiotest.Mock{},
			)
			if err == nil {
				t.Fatal("expected buildDockerImage to return an error when the build fails")
			}
			if !strings.HasPrefix(err.Error(), "docker image build failed (the base image is missing") {
				t.Errorf("error = %v, want the missing package to lead the message", err)
			}
			if !strings.Contains(err.Error(), "newuidmap") {
				t.Errorf("error = %v, want it to name the missing prerequisite", err)
			}
			if strings.Contains(err.Error(), "Last build output") {
				t.Errorf(
					"error = %v, want the duplicate tail block dropped when the marker names the prerequisite",
					err,
				)
			}
		})
	}
}

// TestBuildFailureErrorPreservesTheCause pins that every classification branch,
// and the appended output block, wrap the engine error with %w so errors.Is
// still reaches the cause.
func TestBuildFailureErrorPreservesTheCause(t *testing.T) {
	sentinel := errors.New("engine boom")
	for _, tc := range []struct {
		name        string
		message     string
		buildOutput string
	}{
		{name: "default", message: "boom", buildOutput: "step output"},
		{name: "base image", message: "pull access denied for private-base", buildOutput: "step output"},
		{
			name:        "unresolved name",
			message:     "short-name x did not resolve to an alias and no unqualified search registries are defined",
			buildOutput: "step output",
		},
		{name: "network", message: "Temporary failure resolving 'deb.debian.org'", buildOutput: "step output"},
		{
			name:        "missing rootless prerequisite",
			message:     "The command '/bin/sh -c set -e; ...' returned a non-zero code: 1",
			buildOutput: "error: rootless docker prerequisite missing: newuidmap",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := buildFailureError(fmt.Errorf("%s: %w", tc.message, sentinel), tc.buildOutput)
			if !errors.Is(err, sentinel) {
				t.Errorf("errors.Is(%v, sentinel) = false, want true", err)
			}
		})
	}
}

// TestBuildDockerImageReportsTheStreamedOutputOnFailure pins that a classic
// build failure carries the lines the failing step streamed, so the apt/DNS
// message is visible without --log-level verbose.
func TestBuildDockerImageReportsTheStreamedOutputOnFailure(t *testing.T) {
	a := agentOpencode(t)
	body := `{"stream":"W: Temporary failure resolving 'deb.debian.org'\n"}` + "\n" +
		`{"errorDetail":{"message":"The command '/bin/sh -c apt-get update' returned a non-zero code: 100"}}`
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(body))}, nil
		},
	})
	err := buildDockerImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "v", "base", "id", imageIdentity{}),
		"tag", "label", false, &termiotest.Mock{},
	)
	if err == nil {
		t.Fatal("expected buildDockerImage to return an error when the build fails")
	}
	for _, want := range []string{"Temporary failure resolving 'deb.debian.org'", "image-build-fails"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

// TestBuildFailureErrorClassifiesResolverFailures pins that the resolver
// wordings a base-image pull, npm, or glibc emit are classified as network
// failures, and that the hint does not claim the failure downloaded packages.
func TestBuildFailureErrorClassifiesResolverFailures(t *testing.T) {
	for _, message := range []string{
		"npm ERR! network getaddrinfo EAI_AGAIN registry.npmjs.org",
		"dial tcp: lookup registry-1.docker.io: Temporary failure in name resolution",
		"npm ERR! network getaddrinfo ENOTFOUND registry.npmjs.org",
	} {
		err := buildFailureError(errors.New(message), "")
		if !strings.Contains(err.Error(), "image-build-fails") {
			t.Errorf("error for %q = %v, want the network hint", message, err)
		}
	}
	err := buildFailureError(errors.New("npm ERR! network getaddrinfo EAI_AGAIN registry.npmjs.org"), "")
	if !strings.Contains(err.Error(), "could not reach the network") {
		t.Errorf("error = %v, want the network hint worded for any download, not only packages", err)
	}
}

// TestBuildFailureErrorIgnoresANonNetworkResolveError pins that a bundler's
// module-resolution error is not misreported as a network failure.
func TestBuildFailureErrorIgnoresANonNetworkResolveError(t *testing.T) {
	err := buildFailureError(
		errors.New(`error during build: RollupError: Could not resolve "./module" from "src/main.js"`), "",
	)
	if strings.Contains(err.Error(), "image-build-fails") {
		t.Errorf("error = %v, must not carry the network hint for a module-resolution error", err)
	}
}

// TestBuildOutputTailSplitsMultiLineChunks pins that a chunk carrying several
// lines records each of them, so the cap counts lines rather than chunks.
func TestBuildOutputTailSplitsMultiLineChunks(t *testing.T) {
	var tail buildOutputTail
	tail.add("first\nsecond\n")
	tail.add("third\n\nfourth")
	if got, want := tail.String(), "first\nsecond\nthird\nfourth"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

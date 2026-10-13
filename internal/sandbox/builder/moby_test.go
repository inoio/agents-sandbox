package builder

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

type fakeMobyEngine struct {
	buildFn   func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error)
	inspectFn func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error)
	saveFn    func(context.Context, []string, ...client.ImageSaveOption) (client.ImageSaveResult, error)
	pullFn    func(context.Context, string, client.ImagePullOptions) (io.ReadCloser, error)
	pruneFn   func(context.Context, client.ImagePruneOptions) (client.ImagePruneResult, error)
	pingFn    func(context.Context, client.PingOptions) (client.PingResult, error)

	buildOptions client.ImageBuildOptions
	pruneOptions client.ImagePruneOptions
	inspectCalls int
	pullCalls    int
}

func (f *fakeMobyEngine) ImageBuild(
	ctx context.Context,
	buildContext io.Reader,
	options client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	f.buildOptions = options
	if f.buildFn != nil {
		return f.buildFn(ctx, buildContext, options)
	}
	return emptyBuildResult(), nil
}

func (f *fakeMobyEngine) ImageInspect(
	ctx context.Context,
	imageID string,
	inspectOpts ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	f.inspectCalls++
	if f.inspectFn != nil {
		return f.inspectFn(ctx, imageID, inspectOpts...)
	}
	return client.ImageInspectResult{}, nil
}

func (f *fakeMobyEngine) ImageSave(
	ctx context.Context,
	imageIDs []string,
	saveOpts ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	if f.saveFn != nil {
		return f.saveFn(ctx, imageIDs, saveOpts...)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeMobyEngine) ImagePull(
	ctx context.Context,
	ref string,
	opts client.ImagePullOptions,
) (io.ReadCloser, error) {
	f.pullCalls++
	if f.pullFn != nil {
		return f.pullFn(ctx, ref, opts)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeMobyEngine) ImagePrune(
	ctx context.Context,
	opts client.ImagePruneOptions,
) (client.ImagePruneResult, error) {
	f.pruneOptions = opts
	if f.pruneFn != nil {
		return f.pruneFn(ctx, opts)
	}
	return client.ImagePruneResult{}, nil
}

func (f *fakeMobyEngine) Ping(ctx context.Context, opts client.PingOptions) (client.PingResult, error) {
	if f.pingFn != nil {
		return f.pingFn(ctx, opts)
	}
	return client.PingResult{}, nil
}

func emptyBuildResult() client.ImageBuildResult {
	return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}
}

func inspectResult(id string, env []string) client.ImageInspectResult {
	response := image.InspectResponse{ID: id}
	if env != nil {
		response.Config = &dockerspec.DockerOCIImageConfig{
			Env: env,
		}
	}
	return client.ImageInspectResult{InspectResponse: response}
}

func buildArgValue(args map[string]*string, key string) string {
	if value := args[key]; value != nil {
		return *value
	}
	return ""
}

func testBuildContext(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"Dockerfile":             "FROM debian:trixie-slim\n",
		"agents-sandbox-path.sh": "# merge AGENTS_SANDBOX_IMAGE_PATH\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestMobyBuildContextTarContainsDockerfile(t *testing.T) {
	dir := testBuildContext(t)
	reader, err := buildContextTar(dir)
	if err != nil {
		t.Fatalf("buildContextTar() error = %v", err)
	}

	entries := map[string]string{}
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		content, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatalf("read tar entry %s: %v", header.Name, err)
		}
		entries[header.Name] = string(content)
	}

	if entries["Dockerfile"] != "FROM debian:trixie-slim\n" {
		t.Errorf("Dockerfile entry = %q, want the context Dockerfile", entries["Dockerfile"])
	}
	if entries["agents-sandbox-path.sh"] == "" {
		t.Error("agents-sandbox-path.sh missing from the build context tar")
	}
}

func TestNewMobyBuilderIsBuilder(t *testing.T) {
	restore := newMobyEngine
	t.Cleanup(func() { newMobyEngine = restore })
	newMobyEngine = func() (mobyEngine, error) { return &fakeMobyEngine{}, nil }

	if newMobyBuilder() == nil {
		t.Fatal("newMobyBuilder() = nil, want a Builder")
	}
}

func TestMobyBuildInvokesImageBuild(t *testing.T) {
	engine := &fakeMobyEngine{}
	engine.inspectFn = func(_ context.Context, imageID string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		if imageID == "debian:trixie-slim" {
			return inspectResult("sha256:base", nil), nil
		}
		return inspectResult("sha256:image", nil), nil
	}
	engine.saveFn = func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
		return io.NopCloser(strings.NewReader("archive")), nil
	}

	builder := &mobyBuilder{engine: engine}
	result, err := builder.Build(context.Background(), BuildRequest{
		ContextDir: testBuildContext(t),
		Tag:        "runner:test",
		BaseRef:    "debian:trixie-slim",
		NoCache:    true,
		BuildArgs:  map[string]string{"USER_UID": "1000"},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer func() { _ = os.Remove(result.ArchivePath) }()

	options := engine.buildOptions
	if !slices.Equal(options.Tags, []string{"runner:test"}) {
		t.Errorf("ImageBuildOptions.Tags = %v, want [runner:test]", options.Tags)
	}
	if !options.Remove {
		t.Error("ImageBuildOptions.Remove = false, want true")
	}
	if !options.NoCache {
		t.Error("ImageBuildOptions.NoCache = false, want true")
	}
	if got := buildArgValue(options.BuildArgs, "BASE_IMAGE"); got != "debian:trixie-slim@sha256:base" {
		t.Errorf("BuildArgs[BASE_IMAGE] = %q, want %q", got, "debian:trixie-slim@sha256:base")
	}
	if got := buildArgValue(options.BuildArgs, "USER_UID"); got != "1000" {
		t.Errorf("BuildArgs[USER_UID] = %q, want %q", got, "1000")
	}
}

func TestMobyBuildReturnsSaveArchive(t *testing.T) {
	expected := []byte("saved-image-archive")
	engine := &fakeMobyEngine{}
	engine.inspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return inspectResult("sha256:image", nil), nil
	}
	engine.saveFn = func(_ context.Context, imageIDs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
		if !slices.Equal(imageIDs, []string{"runner:test"}) {
			t.Errorf("ImageSave imageIDs = %v, want [runner:test]", imageIDs)
		}
		return io.NopCloser(bytes.NewReader(expected)), nil
	}

	builder := &mobyBuilder{engine: engine}
	result, err := builder.Build(
		context.Background(),
		BuildRequest{ContextDir: testBuildContext(t), Tag: "runner:test", BaseRef: "debian:trixie-slim"},
	)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer func() { _ = os.Remove(result.ArchivePath) }()

	got, err := os.ReadFile(result.ArchivePath)
	if err != nil {
		t.Fatalf("read archive %q: %v", result.ArchivePath, err)
	}
	if !bytes.Equal(got, expected) {
		t.Errorf("archive contents = %q, want %q", got, expected)
	}
}

func TestMobyBuildResolvesBaseByPullingWhenAbsent(t *testing.T) {
	engine := &fakeMobyEngine{}
	baseInspects := 0
	engine.inspectFn = func(_ context.Context, imageID string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		if imageID != "debian:trixie-slim" {
			return inspectResult("sha256:image", nil), nil
		}
		baseInspects++
		if baseInspects == 1 {
			return client.ImageInspectResult{}, errors.New("No such image: debian:trixie-slim")
		}
		return inspectResult("sha256:base", nil), nil
	}
	engine.pullFn = func(_ context.Context, ref string, _ client.ImagePullOptions) (io.ReadCloser, error) {
		if ref != "debian:trixie-slim" {
			t.Errorf("ImagePull ref = %q, want debian:trixie-slim", ref)
		}
		return io.NopCloser(strings.NewReader(`{"status":"pulled"}`)), nil
	}

	builder := &mobyBuilder{engine: engine}
	result, err := builder.Build(
		context.Background(),
		BuildRequest{ContextDir: testBuildContext(t), Tag: "runner:test", BaseRef: "debian:trixie-slim"},
	)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer func() { _ = os.Remove(result.ArchivePath) }()

	if engine.pullCalls != 1 {
		t.Errorf("ImagePull calls = %d, want 1", engine.pullCalls)
	}
	if baseInspects != 2 {
		t.Errorf("base ImageInspect calls = %d, want 2", baseInspects)
	}
	if got := buildArgValue(engine.buildOptions.BuildArgs, "BASE_IMAGE"); got != "debian:trixie-slim@sha256:base" {
		t.Errorf("BuildArgs[BASE_IMAGE] = %q, want %q", got, "debian:trixie-slim@sha256:base")
	}
}

func TestMobyEngineDownNamesDocker(t *testing.T) {
	restore := newMobyEngine
	t.Cleanup(func() { newMobyEngine = restore })
	newMobyEngine = func() (mobyEngine, error) {
		return nil, errors.New("cannot connect to the Docker daemon")
	}

	builder := &mobyBuilder{}
	_, err := builder.Build(
		context.Background(),
		BuildRequest{ContextDir: testBuildContext(t), Tag: "runner:test", BaseRef: "debian:trixie-slim"},
	)
	if err == nil {
		t.Fatal("Build() error = nil, want a docker-unavailable error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "docker") {
		t.Errorf("error %q does not name Docker", err)
	}
	if !strings.Contains(err.Error(), "msb") {
		t.Errorf("error %q does not say how to switch back to msb", err)
	}
}

func TestMobyPruneMapsToImagePrune(t *testing.T) {
	engine := &fakeMobyEngine{}
	builder := &mobyBuilder{engine: engine}

	if err := builder.PruneCache(context.Background()); err != nil {
		t.Fatalf("PruneCache() error = %v", err)
	}

	filters := engine.pruneOptions.Filters
	if _, ok := filters["dangling"]["true"]; !ok {
		t.Errorf("prune filters = %v, want dangling=true", filters)
	}
	if _, ok := filters["label"]; ok {
		t.Errorf("prune filters = %v, want no label filter (managed label is only on the tagged image)", filters)
	}
	if _, ok := filters["until"]["24h"]; !ok {
		t.Errorf("prune filters = %v, want until=24h", filters)
	}
}

func TestMobyPruneCachePrunesDaemon(t *testing.T) {
	pruneCalls := 0
	engine := &fakeMobyEngine{
		pruneFn: func(context.Context, client.ImagePruneOptions) (client.ImagePruneResult, error) {
			pruneCalls++
			return client.ImagePruneResult{}, nil
		},
	}
	builder := &mobyBuilder{engine: engine}

	if err := builder.PruneCache(context.Background()); err != nil {
		t.Fatalf("PruneCache() error = %v", err)
	}
	if pruneCalls != 1 {
		t.Errorf("ImagePrune calls = %d, want 1", pruneCalls)
	}
}

func TestMobyBuildResultPopulatesDigestEnv(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/root"}
	engine := &fakeMobyEngine{}
	engine.inspectFn = func(_ context.Context, imageID string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		if imageID == "debian:trixie-slim" {
			return inspectResult("sha256:base", nil), nil
		}
		return inspectResult("sha256:image", env), nil
	}

	builder := &mobyBuilder{engine: engine}
	result, err := builder.Build(
		context.Background(),
		BuildRequest{ContextDir: testBuildContext(t), Tag: "runner:test", BaseRef: "debian:trixie-slim"},
	)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer func() { _ = os.Remove(result.ArchivePath) }()

	if result.Digest != "sha256:image" {
		t.Errorf("BuildResult.Digest = %q, want %q", result.Digest, "sha256:image")
	}
	if !slices.Equal(result.Env, env) {
		t.Errorf("BuildResult.Env = %v, want %v", result.Env, env)
	}
	if result.BaseDigest != "debian:trixie-slim@sha256:base" {
		t.Errorf("BuildResult.BaseDigest = %q, want %q", result.BaseDigest, "debian:trixie-slim@sha256:base")
	}
}

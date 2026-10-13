package builder

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/moby/client"
)

// managedImageLabel matches the runner images agents-sandbox builds, so a prune
// reclaims only our own dangling layers.
const managedImageLabel = "org.agents-sandbox.managed=true"

// mobyEngine is the subset of the Docker Engine client the moby backend uses.
// It is an interface so the backend can be exercised without a Docker daemon.
type mobyEngine interface {
	ImageBuild(
		ctx context.Context,
		buildContext io.Reader,
		options client.ImageBuildOptions,
	) (client.ImageBuildResult, error)
	ImageInspect(
		ctx context.Context,
		imageID string,
		inspectOpts ...client.ImageInspectOption,
	) (client.ImageInspectResult, error)
	ImageSave(
		ctx context.Context,
		imageIDs []string,
		saveOpts ...client.ImageSaveOption,
	) (client.ImageSaveResult, error)
	ImagePull(ctx context.Context, ref string, opts client.ImagePullOptions) (io.ReadCloser, error)
	ImagePrune(ctx context.Context, opts client.ImagePruneOptions) (client.ImagePruneResult, error)
	Ping(ctx context.Context, opts client.PingOptions) (client.PingResult, error)
}

// newMobyEngine constructs the Docker Engine client. It is a variable so tests
// can substitute a fake and simulate an unreachable daemon.
var newMobyEngine = realMobyEngine

func realMobyEngine() (mobyEngine, error) {
	mobyClient, err := client.New(clientOptions()...)
	if err != nil {
		return nil, err
	}
	return &realMobyEngineClient{client: mobyClient}, nil
}

// realMobyEngineClient adapts the moby client to the engine seam; the pull
// response is narrowed to an io.ReadCloser, which is all the backend drains.
type realMobyEngineClient struct {
	client *client.Client
}

func (c *realMobyEngineClient) ImageBuild(
	ctx context.Context,
	buildContext io.Reader,
	options client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	return c.client.ImageBuild(ctx, buildContext, options)
}

func (c *realMobyEngineClient) ImageInspect(
	ctx context.Context,
	imageID string,
	inspectOpts ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	return c.client.ImageInspect(ctx, imageID, inspectOpts...)
}

func (c *realMobyEngineClient) ImageSave(
	ctx context.Context,
	imageIDs []string,
	saveOpts ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	return c.client.ImageSave(ctx, imageIDs, saveOpts...)
}

func (c *realMobyEngineClient) ImagePull(
	ctx context.Context,
	ref string,
	opts client.ImagePullOptions,
) (io.ReadCloser, error) {
	return c.client.ImagePull(ctx, ref, opts)
}

func (c *realMobyEngineClient) ImagePrune(
	ctx context.Context,
	opts client.ImagePruneOptions,
) (client.ImagePruneResult, error) {
	return c.client.ImagePrune(ctx, opts)
}

func (c *realMobyEngineClient) Ping(ctx context.Context, opts client.PingOptions) (client.PingResult, error) {
	return c.client.Ping(ctx, opts)
}

// mobyBuilder builds the runner image through a Docker Engine's classic builder
// rather than BuildKit. It delivers a `docker save` archive that the image
// package loads into the microsandbox cache, mirroring the BuildKit backend.
type mobyBuilder struct {
	engine mobyEngine
}

var _ Builder = (*mobyBuilder)(nil)

func newMobyBuilder() Builder {
	return &mobyBuilder{}
}

// resolveEngine returns the lazily constructed engine, failing with an
// actionable message when no Docker daemon is reachable.
func (b *mobyBuilder) resolveEngine(ctx context.Context) (mobyEngine, error) {
	if b.engine != nil {
		return b.engine, nil
	}
	engine, err := newMobyEngine()
	if err != nil {
		return nil, dockerUnavailableError(err)
	}
	if _, err := engine.Ping(ctx, client.PingOptions{}); err != nil {
		return nil, dockerUnavailableError(err)
	}
	b.engine = engine
	return engine, nil
}

// dockerUnavailableError names the missing dependency and the way back to the
// default microsandbox BuildKit backend.
func dockerUnavailableError(cause error) error {
	return fmt.Errorf(
		"docker engine unavailable: %w; "+
			"set builder=msb in the launcher config or AGENTS_SANDBOX_BUILDER=msb to build with microsandbox BuildKit",
		cause,
	)
}

func (b *mobyBuilder) Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	engine, err := b.resolveEngine(ctx)
	if err != nil {
		return BuildResult{}, err
	}

	baseDigest, err := resolveMobyBaseDigest(ctx, engine, req.BaseRef)
	if err != nil {
		return BuildResult{}, fmt.Errorf("resolve base image %s: %w", req.BaseRef, err)
	}

	buildContext, err := buildContextTar(req.ContextDir)
	if err != nil {
		return BuildResult{}, err
	}

	buildResult, err := engine.ImageBuild(ctx, buildContext, client.ImageBuildOptions{
		Tags:      []string{req.Tag},
		Remove:    true,
		NoCache:   req.NoCache,
		BuildArgs: mobyBuildArgs(req.BuildArgs, baseDigest),
	})
	if err != nil {
		return BuildResult{}, fmt.Errorf("docker image build failed: %w", err)
	}
	defer func() { _ = buildResult.Body.Close() }()

	if drainErr := drainBuildOutput(buildResult.Body, req.Progress); drainErr != nil {
		return BuildResult{}, fmt.Errorf("docker image build failed: %w", drainErr)
	}
	inspect, err := engine.ImageInspect(ctx, req.Tag)
	if err != nil {
		return BuildResult{}, fmt.Errorf("inspect built image %s: %w", req.Tag, err)
	}

	archivePath, err := writeImageSaveArchive(ctx, engine, req.Tag)
	if err != nil {
		return BuildResult{}, err
	}

	env := []string(nil)
	if inspect.Config != nil {
		env = inspect.Config.Env
	}

	return BuildResult{
		ArchivePath: archivePath,
		Digest:      inspect.ID,
		Env:         env,
		BaseDigest:  baseDigest,
	}, nil
}

func (b *mobyBuilder) PruneCache(ctx context.Context) error {
	engine, err := b.resolveEngine(ctx)
	if err != nil {
		return err
	}
	filters := client.Filters{}.
		Add("dangling", "true").
		Add("label", managedImageLabel).
		Add("until", "24h")
	//exhaustruct:ignore // only the filter set matters for a dangling-image prune
	options := client.ImagePruneOptions{Filters: filters}
	if _, err := engine.ImagePrune(ctx, options); err != nil {
		return fmt.Errorf("prune docker images: %w", err)
	}
	return nil
}

// mobyBuildArgs converts the request build args into Docker's pointer-valued
// form and adds the digest-pinned base image, which the Dockerfile records.
func mobyBuildArgs(args map[string]string, baseDigest string) map[string]*string {
	buildArgs := make(map[string]*string, len(args)+1)
	for key, value := range args {
		buildArgs[key] = &value
	}
	buildArgs["BASE_IMAGE"] = &baseDigest
	return buildArgs
}

// resolveMobyBaseDigest returns "<baseRef>@<imageID>" for the base image,
// pulling it first when it is not present locally. The FROM itself stays a tag
// so it floats; the digest records what the image was built from.
func resolveMobyBaseDigest(ctx context.Context, engine mobyEngine, baseRef string) (string, error) {
	inspect, err := engine.ImageInspect(ctx, baseRef)
	if err != nil {
		pulled, pullErr := pullBaseImage(ctx, engine, baseRef)
		if pullErr != nil {
			return "", pullErr
		}
		inspect = pulled
	}
	return baseRef + "@" + inspect.ID, nil
}

// pullBaseImage pulls baseRef and inspects it once the pull completes.
func pullBaseImage(ctx context.Context, engine mobyEngine, baseRef string) (client.ImageInspectResult, error) {
	pull, err := engine.ImagePull(ctx, baseRef, client.ImagePullOptions{})
	if err != nil {
		return client.ImageInspectResult{}, fmt.Errorf("pull base image %s: %w", baseRef, err)
	}
	defer func() { _ = pull.Close() }()
	if _, copyErr := io.Copy(io.Discard, pull); copyErr != nil {
		return client.ImageInspectResult{}, fmt.Errorf("pull base image %s: %w", baseRef, copyErr)
	}
	inspect, err := engine.ImageInspect(ctx, baseRef)
	if err != nil {
		return client.ImageInspectResult{}, fmt.Errorf("inspect pulled base image %s: %w", baseRef, err)
	}
	return inspect, nil
}

// writeImageSaveArchive streams a `docker save` of tag into a temp file and
// returns its path. The image package owns removing the file once loaded.
func writeImageSaveArchive(ctx context.Context, engine mobyEngine, tag string) (string, error) {
	saveResult, err := engine.ImageSave(ctx, []string{tag})
	if err != nil {
		return "", fmt.Errorf("export docker image %s: %w", tag, err)
	}
	defer func() { _ = saveResult.Close() }()

	archive, err := os.CreateTemp("", "agents-sandbox-image-*.tar")
	if err != nil {
		return "", fmt.Errorf("create image archive: %w", err)
	}
	if _, err := io.Copy(archive, saveResult); err != nil {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
		return "", fmt.Errorf("write image archive: %w", err)
	}
	if err := archive.Close(); err != nil {
		_ = os.Remove(archive.Name())
		return "", fmt.Errorf("close image archive: %w", err)
	}
	return archive.Name(), nil
}

// buildContextTar streams the build context directory as a tar, which is the
// transport the Docker Engine build endpoint expects.
func buildContextTar(dir string) (io.Reader, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read build context %s: %w", dir, err)
	}
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := writeTarEntry(writer, dir, entry.Name()); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close build context tar: %w", err)
	}
	return &buffer, nil
}

func writeTarEntry(writer *tar.Writer, dir, name string) error {
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat build context file %s: %w", name, err)
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("tar header for %s: %w", name, err)
	}
	header.Name = name
	if writeErr := writer.WriteHeader(header); writeErr != nil {
		return fmt.Errorf("tar write header for %s: %w", name, writeErr)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open build context file %s: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("tar write %s: %w", name, err)
	}
	return nil
}

// mobyBuildMessage is one line of the Docker build API's JSON progress stream.
type mobyBuildMessage struct {
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Error  string `json:"error"`
	Stream string `json:"stream"`
}

// drainBuildOutput decodes the Docker build stream, forwarding each non-empty
// output line to progress and returning the first reported build error.
func drainBuildOutput(reader io.Reader, progress func(string)) error {
	decoder := json.NewDecoder(reader)
	for {
		var message mobyBuildMessage
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("unexpected docker build output: %w", err)
		}
		if message.Error != "" {
			return errors.New(message.Error)
		}
		if message.ErrorDetail.Message != "" {
			return errors.New(message.ErrorDetail.Message)
		}
		if progress == nil {
			continue
		}
		if line := strings.Trim(message.Stream, " \n"); line != "" {
			progress(line)
		}
	}
}

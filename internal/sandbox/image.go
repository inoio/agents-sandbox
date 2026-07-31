package sandbox

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	sysio "io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	BaseTag        = "opencode-msb/runner-base:latest"
	DindBaseTag    = "opencode-msb/runner-base-dind:latest"
	dockerfileMode = 0o644
)

type dockerClient interface {
	ImageBuild(
		ctx context.Context,
		buildContext sysio.Reader,
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
	ImageRemove(
		ctx context.Context,
		imageID string,
		opts client.ImageRemoveOptions,
	) (client.ImageRemoveResult, error)
	ImageTag(
		ctx context.Context,
		opts client.ImageTagOptions,
	) (client.ImageTagResult, error)
	Close() error
}

func ReferencesBase(dockerfile []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if strings.HasPrefix(line, "FROM") && strings.Contains(line, BaseTag) {
			return true
		}
	}
	return false
}

func ReferencesDindBase(dockerfile []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if strings.HasPrefix(line, "FROM") && strings.Contains(line, DindBaseTag) {
			return true
		}
	}
	return false
}

func ImageTag(projectSlug, imageDigest string) string {
	return "opencode-msb/runner-" + projectSlug + ":" + git.HashID(imageDigest)
}

func dockerfileTar(dockerfile []byte) *bytes.Buffer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Mode: dockerfileMode,
		Size: int64(len(dockerfile)),
	})
	_, _ = tw.Write(dockerfile)
	_ = tw.Close()
	return &buf
}

type dockerBuildMessage struct {
	Stream      string `json:"stream"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Error string `json:"error"`
}

// userBuildArgs returns Docker build arguments that align the in-image dev user
// (see data/Dockerfile's USER_UID/USER_GID) with the host user that owns the
// bind-mounted /workspace, avoiding permission mismatches inside the VM.
func userBuildArgs(uid, gid int) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	return map[string]*string{
		"USER_UID": &u,
		"USER_GID": &g,
	}
}

func runnerTag(projectSlug string) string {
	return "opencode-msb/runner-" + projectSlug + ":latest"
}

// envDir returns the project-local metadata directory for image env info.
func envDir() string {
	return ".opencode-msb"
}

// envMetaFile returns the JSON file path for image env metadata, keyed by the
// stable tag so the data survives image rebuilds and docker prune.
func envMetaFile(tag string) string {
	return filepath.Join(envDir(), "image-env-"+tag+".json")
}

// storeImageEnv writes image env vars to a JSON file.
// Survives docker prune and image rebuilds.
func storeImageEnv(tag string, envs map[string]string) {
	if tag == "" || len(envs) == 0 {
		return
	}
	dir := envDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	data, _ := json.Marshal(envs)
	_ = os.WriteFile(envMetaFile(tag), data, 0o600)
}

// loadImageEnv reads a previously stored image env map from the cached JSON
// file keyed by tag. Returns nil if no file exists.
func loadImageEnv(tag string) map[string]string {
	path := envMetaFile(tag)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// ensureRunnerImage checks the runner Docker image on disk, loads stored env
// metadata as fallback when the image has been pruned, builds it if needed,
// and returns the tag, digest, and image env map.
func ensureRunnerImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui stdio.UI,
) (string, string, map[string]string, error) {
	rTag := runnerTag(projectSlug)
	mustBuild := force
	imageEnv := make(map[string]string)
	var imageDigest string

	if !force {
		_, inspectErr := cli.ImageInspect(ctx, rTag)
		if inspectErr != nil {
			ui.Verbosef("image inspect failed (might be pruned): %v", inspectErr)
			if cached := loadImageEnv(rTag); cached != nil {
				imageEnv = cached
				ui.Verbosef("using stored image env metadata for %s", rTag)
			}
			mustBuild = true
		} else {
			inspect, err := cli.ImageInspect(ctx, rTag)
			if err == nil {
				imageDigest = inspect.ID
				imageEnv = parseImageEnv(inspect.Config.Env)
				storeImageEnv(rTag, imageEnv)
				ui.Verbosef("inspected image %s with %d env vars", rTag, len(imageEnv))
			}
		}
	}

	if mustBuild {
		if err := buildDockerImage(ctx, cli, dockerfile, rTag, "Building runner image", force, ui); err != nil {
			if len(imageEnv) > 0 {
				ui.Warnf("build succeeded but inspect failed; using stored env metadata")
				return rTag, imageDigest, imageEnv, nil
			}
			return "", "", nil, err
		}
		inspect, err := cli.ImageInspect(ctx, rTag)
		if err != nil {
			return "", "", nil, fmt.Errorf("cannot inspect built image: %w", err)
		}
		imageDigest = inspect.ID
		imageEnv = parseImageEnv(inspect.Config.Env)
		storeImageEnv(rTag, imageEnv)
		ui.Verbosef("rebuilt image %s with %d env vars", rTag, len(imageEnv))

		// Also tag with digest for proper cleanup during prune
		digestTag := ImageTag(projectSlug, imageDigest)
		if digestTag != rTag {
			if _, err := cli.ImageTag(ctx, client.ImageTagOptions{Source: rTag, Target: digestTag}); err != nil {
				ui.Warnf("failed to tag image with digest: %v", err)
			} else {
				ui.Verbosef("tagged image with digest: %s", digestTag)
			}
		}
	}

	return rTag, imageDigest, imageEnv, nil
}

// EnsureImage builds/inspects the runner Docker image and returns its tag,
// digest, and the Dockerfile ENV directives baked into the image config as a
// map. The env map is derived from cli.ImageInspect; if the image is no
// longer on disk (e.g. after `docker prune`), it falls back to stored JSON
// metadata written by a previous invokation.
func EnsureImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui stdio.UI,
) (string, string, map[string]string, error) {
	if force || ReferencesBase(dockerfile) || ReferencesDindBase(dockerfile) {
		if err := buildDockerImage(
			ctx,
			cli,
			EmbeddedDockerfile,
			BaseTag,
			"Building base runner image",
			force,
			ui,
		); err != nil {
			return "", "", nil, fmt.Errorf("building base image: %w", err)
		}
	}

	if ReferencesDindBase(dockerfile) {
		if err := buildDockerImage(
			ctx,
			cli,
			EmbeddedDindDockerfile,
			DindBaseTag,
			"Building Docker-in-Docker base runner image",
			force,
			ui,
		); err != nil {
			return "", "", nil, fmt.Errorf("building dind base image: %w", err)
		}
	}

	rTag, imageDigest, imageEnv, err := ensureRunnerImage(ctx, cli, dockerfile, projectSlug, force, ui)
	if err != nil {
		return "", "", nil, err
	}

	imageRef := ImageTag(projectSlug, imageDigest)

	_, cacheErr := msb.Image.Get(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, imageEnv, nil
	}

	spin := ui.Spinner("Loading image into microsandbox")
	saveResult, err := cli.ImageSave(ctx, []string{rTag})
	if err != nil {
		spin.StopError(err)
		return "", "", nil, fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()

	cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
	cmd.Stdin = saveResult
	if out, err := cmd.CombinedOutput(); err != nil {
		spin.StopError(err)
		return "", "", nil, fmt.Errorf("loading image into microsandbox failed: %w: %s", err, out)
	}
	spin.Stop()

	return imageRef, imageDigest, imageEnv, nil
}

func parseImageEnv(envs []string) map[string]string {
	out := make(map[string]string, len(envs))
	for _, e := range envs {
		if i := strings.Index(e, "="); i > 0 {
			out[e[:i]] = e[i+1:]
		}
	}
	return out
}

func buildDockerImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	tag, label string,
	force bool,
	ui stdio.UI,
) error {
	spin := ui.Spinner(label)

	buildResp, err := cli.ImageBuild(ctx, dockerfileTar(dockerfile), client.ImageBuildOptions{
		Tags:      []string{tag},
		Remove:    true,
		NoCache:   force,
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid()),
	})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("docker image build failed: %w", err)
	}

	buildErr := scanBuildOutput(buildResp.Body, ui)
	_ = buildResp.Body.Close()
	if buildErr != nil {
		spin.StopError(buildErr)
		if strings.Contains(buildErr.Error(), "pull access denied") {
			return fmt.Errorf("docker image build failed (base image not found or not logged in): %w", buildErr)
		}
		return fmt.Errorf("docker image build failed: %w", buildErr)
	}

	spin.Stop()
	return nil
}

func scanBuildOutput(r sysio.Reader, ui stdio.UI) error {
	dec := json.NewDecoder(r)
	for {
		var msg dockerBuildMessage
		if err := dec.Decode(&msg); err != nil {
			if err == sysio.EOF {
				return nil
			}
			return fmt.Errorf("unexpected Docker build output: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("%s", msg.Error)
		}
		if msg.ErrorDetail.Message != "" {
			return fmt.Errorf("%s", msg.ErrorDetail.Message)
		}
		if msg.Stream != "" {
			ui.Verbosef("%s", strings.TrimSuffix(msg.Stream, "\n"))
		}
	}
}

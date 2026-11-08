package image

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/docslink"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// buildRequest is the single description of one runner-image build: the rendered
// plan (Dockerfile, content identity, dockerfile-id, and the resolved agent
// release baked as the installer ARG default), the resolved base digest recorded
// as provenance, and the resolved docker setting. Every build arg is derived from
// this one object instead of parallel positional values.
//
// The one resolved options.DockerSetting feeds two deliberate, different docker
// derivations, and they must not be "simplified" into each other: the
// DOCKER_VERSION arg (userBuildArgs) reads the raw docker.Enabled, while the
// IDENTITY_DOCKER label reads the effective value (effectiveDocker, recorded in
// plan.identity.Docker). They diverge whenever the setting is not explicit but the
// project Dockerfile's final-stage marker or managed docker base implies docker:
// then DOCKER_VERSION is absent while IDENTITY_DOCKER is "true". Unifying the two
// would change the build args.
//
// baseDigest and plan.identity.BaseImage are deliberately named apart: the former
// is the resolved `<ref>@<image id>` the build records, the latter is the locally
// cached base the build decision compares against.
type buildRequest struct {
	plan       buildPlan
	baseDigest string
	docker     options.DockerSetting
}

// buildDockerImage builds the described image as a tag, wrapping the build with a
// spinner and verbose output. noCache bypasses Docker's layer cache; it is
// independent of whether a build is needed at all.
func buildDockerImage(
	ctx context.Context,
	dclient docker.Client,
	a agent.Agent,
	req buildRequest,
	tag, label string,
	noCache bool,
	ui termio.UI,
) error {
	spinner := ui.Spinner(label)
	line := func(s string) { ui.Verbose(s) }
	ui.Verbosef("Building Docker image (noCache=%v)", noCache)
	if err := buildImage(ctx, dclient, a, req, tag, noCache, line); err != nil {
		spinner.StopError(err)
		return err
	}
	spinner.Stop()
	return nil
}

// buildImage builds a Docker image via dclient.ImageBuild and reports each
// decoded build output line to the given callback. noCache bypasses Docker's layer
// cache; it is independent of whether a build is needed at all.
func buildImage(
	ctx context.Context,
	dclient docker.Client,
	a agent.Agent,
	req buildRequest,
	tag string,
	noCache bool,
	line func(string),
) error {
	// A project input that changed since its approval was read as unapproved;
	// nothing built from this run's inputs may reach the image.
	if err := projectguard.Violation(); err != nil {
		return err
	}
	buildResp, err := dclient.ImageBuild(ctx, buildContextTar(req.plan.rendered), client.ImageBuildOptions{
		Tags:      []string{tag},
		Remove:    true,
		NoCache:   noCache,
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid(), a.ImageSpec(), req),
	})
	if err != nil {
		return buildFailureError(err)
	}
	defer buildResp.Body.Close()

	buildErr := scanBuildOutput(buildResp.Body, line)
	if buildErr != nil {
		return buildFailureError(buildErr)
	}

	return nil
}

// buildFailureError classifies an engine build failure and tailors the message
// for the two a user can act on: a base image the engine could not resolve
// (Docker Engine reports "pull access denied"), and an image name with no
// registry that the engine refused to resolve at all — podman does this when it
// has no unqualified-search-registries, which Docker Engine never needs because
// it assumes Docker Hub. Both point at the troubleshooting section naming the
// remedies. Any other failure stays a plain wrap.
func buildFailureError(err error) error {
	switch msg := err.Error(); {
	case strings.Contains(msg, "pull access denied"):
		return fmt.Errorf(
			"docker image build failed (base image not found or not logged in): %w\n  Docs: %s",
			err,
			docslink.SectionURL("troubleshooting", "the-base-image-cannot-be-found"),
		)
	case isUnresolvedImageName(msg):
		return fmt.Errorf(
			"docker image build failed (the engine could not resolve an image name that has no registry): %w\n  Docs: %s",
			err,
			docslink.SectionURL("troubleshooting", "the-base-image-cannot-be-found"),
		)
	default:
		return fmt.Errorf("docker image build failed: %w", err)
	}
}

// isUnresolvedImageName reports whether the engine refused an unqualified image
// name for want of a registry to resolve it against.
func isUnresolvedImageName(msg string) bool {
	return strings.Contains(msg, "short-name") || strings.Contains(msg, "unqualified search registries")
}

// scanBuildOutput decodes the Docker build API stream and returns errors,
// forwarding each non-empty line of output to the given callback.
func scanBuildOutput(r io.Reader, line func(string)) error {
	dec := json.NewDecoder(r)
	for {
		var msg dockerBuildMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
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
		trimmedMsg := strings.Trim(msg.Stream, " \n")
		if trimmedMsg != "" {
			line(trimmedMsg)
		}
	}
}

const dockerfileMode = 0o644

// executableFileMode marks a build-context file that the image installs as a
// runnable command. Docker's COPY takes the installed file's permission bits
// from the context entry, so the mode has to be set here.
const executableFileMode = 0o755

// buildContextFile is one non-Dockerfile file in the synthetic build context.
type buildContextFile struct {
	name    string
	content []byte
	mode    int64
}

// buildContextFiles returns the synthetic build context's files in write order.
// It is the single source of truth for the context tar and the image identity,
// so a new asset is both shipped and identity-invalidating.
func buildContextFiles() []buildContextFile {
	return []buildContextFile{
		{pathMergeAsset, pathMergeScript(), dockerfileMode},
		{trustHelperAsset, trustHelperScript(), executableFileMode},
		{trustHelperLicenseAsset, trustHelperLicense(), dockerfileMode},
		{hostOpenerShimAsset, hostOpenerShimScript(), executableFileMode},
	}
}

// writeDockerfileTar writes the synthetic build context — the rendered
// Dockerfile followed by the build-context files — to w as a tar stream.
func writeDockerfileTar(w io.Writer, dockerfile []byte) error {
	tw := tar.NewWriter(w)
	if err := writeTarEntry(tw, "Dockerfile", dockerfile, dockerfileMode); err != nil {
		_ = tw.Close()
		return err
	}
	for _, file := range buildContextFiles() {
		if err := writeTarEntry(tw, file.name, file.content, file.mode); err != nil {
			_ = tw.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("tar close: %w", err)
	}
	return nil
}

// buildContextTar returns the build context for dockerfile as an in-memory tar.
// Writing the fixed entries into a bytes.Buffer cannot fail, so a failure is a
// programming error.
func buildContextTar(dockerfile []byte) *bytes.Buffer {
	var buildContext bytes.Buffer
	mustWriteBuildContext(writeDockerfileTar(&buildContext, dockerfile))
	return &buildContext
}

// mustWriteBuildContext panics on an in-memory build-context write error.
func mustWriteBuildContext(err error) {
	if err != nil {
		panic(fmt.Sprintf("write in-memory build context: %v", err))
	}
}

// writeTarEntry writes one regular file to the context tar.
func writeTarEntry(tw *tar.Writer, name string, content []byte, mode int64) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(content)),
	}); err != nil {
		return fmt.Errorf("tar write header: %w", err)
	}
	if _, err := io.Copy(tw, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("tar write %s: %w", name, err)
	}
	return nil
}

// userBuildArgs returns Docker build arguments that align the in-image dev
// user with the host user that owns the bind-mounted /workspace, pin the agent
// version, record the base image provenance label, and carry the identity
// components recorded as image labels. Every value comes from the request. The
// DOCKER_VERSION arg deliberately follows the request's raw docker.Enabled, not
// the identity's effective docker value (see buildRequest): the two differ for a
// non-explicit setting that the project Dockerfile nonetheless enables, and
// switching this to the effective value would change the build args.
func userBuildArgs(uid, gid int, spec agent.ImageSpec, req buildRequest) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	identity := req.plan.identity
	version := req.plan.agentVersion
	args := map[string]*string{
		"USER_UID":               &u,
		"USER_GID":               &g,
		spec.VersionArg:          &version,
		"BASE_IMAGE":             &req.baseDigest,
		"DOCKERFILE_ID":          &req.plan.dockerfileID,
		projectDockerfileHashArg: &identity.ProjectDockerfileHash,
		agentVersionArg:          &identity.AgentVersion,
		dockerArg:                &identity.Docker,
		userArg:                  &identity.User,
	}
	if req.docker.Enabled {
		v := dockerVersion
		args["DOCKER_VERSION"] = &v
	}
	return args
}

// dockerBuildMessage represents a single line from the Docker build API
// streaming response, decoding both progress and error output.
// Docker returns either an ErrorDetail with a Message or an Error.
type dockerBuildMessage struct {
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Error  string `json:"error"`
	Stream string `json:"stream"`
}

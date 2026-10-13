package builder

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

const (
	dockerBuilderContainer = "agents-sandbox-buildkit-harness"
	dockerBuilderVolume    = "agents-sandbox-buildkit-harness-cache"
)

// dockerCLI runs the docker CLI and returns its combined output. It is the
// seam the Docker backend drives instead of shelling out directly.
type dockerCLI interface {
	run(ctx context.Context, args ...string) (string, error)
}

// realDockerCLI runs the docker binary from PATH.
type realDockerCLI struct{}

var _ dockerCLI = realDockerCLI{}

func (realDockerCLI) run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ResetDockerHarness removes the Docker test-harness builder container and its
// cache volume, treating already-absent resources as success. The integration
// harness calls it before and after a run so a stale container cannot leak.
func ResetDockerHarness(ctx context.Context) error {
	return removeDockerHarness(ctx, realDockerCLI{})
}

// removeDockerHarness removes the harness container and cache volume through the
// given CLI. Missing resources are ignored; the last other error is returned.
func removeDockerHarness(ctx context.Context, d dockerCLI) error {
	var lastErr error
	for _, args := range [][]string{
		{"rm", "-f", dockerBuilderContainer},
		{"volume", "rm", "-f", dockerBuilderVolume},
	} {
		if _, err := d.run(ctx, args...); err != nil && !isMissingResourceError(err) {
			lastErr = err
		}
	}
	return lastErr
}

// isMissingResourceError reports whether a docker error indicates the target
// container or volume does not exist.
func isMissingResourceError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "no such")
}

// dockerBackend drives a buildkitd endpoint running in a plain Docker container,
// used by the test harness. It mirrors the msb backend's cache mount and native
// snapshotter so builds behave the same on either backend.
type dockerBackend struct {
	docker dockerCLI
	port   int
}

var _ backend = (*dockerBackend)(nil)

// Export names a Docker image so the solved result lands in the daemon's image
// store under the requested tag.
func (b *dockerBackend) Export(tag string) exportSpec {
	return exportSpec{Type: "docker", Attrs: map[string]string{"name": tag}}
}

// KeepAlive is a no-op: the Docker harness container has no idle reaper, so it
// stays up regardless.
func (b *dockerBackend) KeepAlive(context.Context) func() {
	return func() {}
}

// Ensure returns the host port of a ready Docker-backed buildkitd, starting and
// waiting for a container when none is already running.
func (b *dockerBackend) Ensure(ctx context.Context) (int, error) {
	if b.port != 0 {
		return b.port, nil
	}
	if port, running := b.runningPort(ctx); running {
		return port, nil
	}

	// A previous harness run may have left a stopped container behind; a missing
	// container is equally fine, so the removal error is ignored.
	_, _ = b.docker.run(ctx, "rm", "-f", dockerBuilderContainer)
	if _, err := b.docker.run(ctx, "volume", "create", dockerBuilderVolume); err != nil {
		return 0, err
	}

	hostPort := options.FirstFreeHostPort(builderHostPortBase)
	runArgs := []string{
		"run", "-d",
		"--privileged",
		"--name", dockerBuilderContainer,
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, builderGuestPort),
		"-v", fmt.Sprintf("%s:%s", dockerBuilderVolume, cacheMountPath),
		builderImage,
		"--oci-worker-snapshotter", "native",
		"--addr", fmt.Sprintf("tcp://0.0.0.0:%d", builderGuestPort),
	}
	if _, err := b.docker.run(ctx, runArgs...); err != nil {
		return 0, err
	}

	if err := waitForBuildkit(ctx, fmt.Sprintf("tcp://127.0.0.1:%d", hostPort)); err != nil {
		return 0, err
	}
	b.port = hostPort
	return hostPort, nil
}

// PruneCache reclaims stale BuildKit cache in the harness container. A stopped
// or absent container has nothing to reclaim, so it is a no-op.
func (b *dockerBackend) PruneCache(ctx context.Context) error {
	if _, running := b.runningPort(ctx); !running {
		return nil
	}
	_, err := b.docker.run(ctx, "exec", dockerBuilderContainer, "buildctl",
		"--addr", fmt.Sprintf("tcp://127.0.0.1:%d", builderGuestPort),
		"prune", "--keep-duration", "24h", "--all")
	return err
}

// runningPort reports the published host port of a running harness container.
func (b *dockerBackend) runningPort(ctx context.Context) (int, bool) {
	out, err := b.docker.run(ctx, "port", dockerBuilderContainer, fmt.Sprintf("%d/tcp", builderGuestPort))
	if err != nil {
		return 0, false
	}
	port, err := parsePublishedPort(out)
	if err != nil {
		return 0, false
	}
	return port, true
}

// parsePublishedPort extracts the host port from `docker port` output of the
// form `127.0.0.1:<port>`.
func parsePublishedPort(output string) (int, error) {
	line := strings.TrimSpace(output)
	line = strings.Split(line, "\n")[0]
	_, portText, ok := strings.Cut(line, ":")
	if !ok {
		return 0, fmt.Errorf("unexpected docker port output %q", output)
	}
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil {
		return 0, fmt.Errorf("parse docker port output %q: %w", output, err)
	}
	return port, nil
}

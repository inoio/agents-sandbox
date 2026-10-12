package builder

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
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

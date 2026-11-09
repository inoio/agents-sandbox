package doctor

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// SetEnsureInstalled replaces the msb ensure-installed seam the doctor tests
// reach checkMsb through, and returns the previous value so a caller can
// restore it.
//
//deadcode:ignore reason="test seam: only doctortest sets it"
func SetEnsureInstalled(fn func(ctx context.Context) error) func(ctx context.Context) error {
	prev := ensureInstalledFunc
	ensureInstalledFunc = fn
	return prev
}

// SetCheckAll replaces the CheckAll seam and returns the previous value so a
// caller can restore it.
//
//deadcode:ignore reason="test seam: only doctortest sets it"
func SetCheckAll(
	fn func(context.Context, docker.Client, termio.Logger) bool,
) func(context.Context, docker.Client, termio.Logger) bool {
	prev := checkAllFunc
	checkAllFunc = fn
	return prev
}

// SetCheckDocker replaces the CheckDocker seam and returns the previous value so
// a caller can restore it.
//
//deadcode:ignore reason="test seam: only doctortest sets it"
func SetCheckDocker(fn func(context.Context, docker.Client) error) func(context.Context, docker.Client) error {
	prev := checkDockerFunc
	checkDockerFunc = fn
	return prev
}

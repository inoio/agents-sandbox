package builder

import (
	"context"
	"os"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// builderBackendEnv selects the active backend at construction time. Only the
// exact value docker selects the Docker harness backend; anything else, including
// an empty or unknown value, falls back to the production msb backend.
const builderBackendEnv = "AGENTS_SANDBOX_BUILDER"

const (
	builderBackendMsb    = "msb"
	builderBackendDocker = "docker"
)

// exportSpec describes how the client exports a solved image: the exporter type
// and its attributes. It lets each backend choose its export format without the
// client hardcoding one.
type exportSpec struct {
	Type  string
	Attrs map[string]string
}

// backend acquires a ready buildkitd endpoint and owns backend-specific cache
// maintenance and export selection.
type backend interface {
	Ensure(ctx context.Context) (int, error)
	PruneCache(ctx context.Context) error
	Export(tag string) exportSpec
}

// msbBackend acquires a buildkitd endpoint from a microsandbox VM. It is the
// production backend.
type msbBackend struct {
	vm *VM
}

func (b *msbBackend) Ensure(ctx context.Context) (int, error) {
	return b.vm.Ensure(ctx)
}

func (b *msbBackend) PruneCache(ctx context.Context) error {
	return b.vm.PruneCache(ctx)
}

// Export selects the untagged OCI archive that microsandbox's ImageLoad
// consumes; the tag is carried explicitly on load rather than in the export.
func (b *msbBackend) Export(string) exportSpec {
	return exportSpec{Type: "oci", Attrs: nil}
}

// newBackend is the single construction point for the active backend. It reads
// the selection environment variable at call time so tests and embedded callers
// can choose a backend without package-level state.
func newBackend() backend {
	if os.Getenv(builderBackendEnv) == builderBackendDocker {
		return &dockerBackend{docker: realDockerCLI{}}
	}
	vm := newVM()
	return &msbBackend{vm: vm}
}

// newVM builds the production builder VM with its readiness seam wired.
func newVM() *VM {
	vm := &VM{Msb: msb.Get(), Ready: nil}
	vm.Ready = vm.defaultReady
	return vm
}

package builder

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
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
	// KeepAlive refreshes the builder's idle-activity timer while a build is in
	// flight so a short idle timeout does not reap it mid-build. It returns a
	// stop function to call when the build finishes.
	KeepAlive(ctx context.Context) func()
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

// KeepAlive keeps the shared builder VM's idle timer refreshed from the VM's
// own touch loop.
func (b *msbBackend) KeepAlive(ctx context.Context) func() {
	return b.vm.KeepAlive(ctx)
}

// Export selects the untagged OCI archive that microsandbox's ImageLoad
// consumes; the tag is carried explicitly on load rather than in the export. The
// archival compression is overridden (see builderCompression): the default gzip
// dominates a large image's export time.
func (b *msbBackend) Export(string) exportSpec {
	attrs := map[string]string{"oci-mediatypes": buildkitAttrTrue}
	applyCompressionAttrs(attrs)
	return exportSpec{Type: "oci", Attrs: attrs}
}

// newBackend is the single construction point for the active backend.
func newBackend() backend {
	vm := newVM()
	return &msbBackend{vm: vm}
}

// newVM builds the production builder VM with its readiness seam wired.
func newVM() *VM {
	vm := &VM{Msb: msb.Get(), Ready: nil}
	vm.Ready = vm.defaultReady
	return vm
}

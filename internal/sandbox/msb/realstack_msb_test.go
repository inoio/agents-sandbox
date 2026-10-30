//go:build msb

package msb

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// The msb-tagged tests drive the real SDK and host runtime through
// realMsbClient — the only place the adapter-over-SDK behavior actually runs.
// They are host-only (`make test-msb`) because they need the native library and
// the developer's real microsandbox state; the untagged unit tests cover the
// same adapters through the seam vars with fakes and never load the library.

// TestRealClientVolumeLifecycle round-trips a volume against the real runtime.
func TestRealClientVolumeLifecycle(t *testing.T) {
	ensureIsolatedRuntime(t)
	client := &realMsbClient{}
	ctx := context.Background()
	name := "agents-sandbox-msb-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	created, err := client.CreateVolume(ctx, name)
	if err != nil {
		t.Fatalf("CreateVolume(%q) error = %v", name, err)
	}
	if created == nil {
		t.Fatal("CreateVolume returned a nil handle")
	}
	t.Cleanup(func() { _ = client.RemoveVolume(context.Background(), name) })

	volumes, err := client.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
	if !volumeNamed(volumes, name) {
		t.Errorf("ListVolumes() did not include the created volume %q", name)
	}
	if _, err := client.GetVolume(ctx, name); err != nil {
		t.Errorf("GetVolume(%q) error = %v", name, err)
	}
	if err := client.RemoveVolume(ctx, name); err != nil {
		t.Errorf("RemoveVolume(%q) error = %v", name, err)
	}
}

// TestRealClientListsSandboxesAndImages drives the real list adapters against
// the host runtime.
func TestRealClientListsSandboxesAndImages(t *testing.T) {
	ensureIsolatedRuntime(t)
	client := &realMsbClient{}
	ctx := context.Background()

	if _, err := client.ListSandboxes(ctx, nil); err != nil {
		t.Errorf("ListSandboxes() error = %v", err)
	}
	if _, err := client.ImageList(ctx); err != nil {
		t.Errorf("ImageList() error = %v", err)
	}
}

func volumeNamed(handles []VolumeHandle, name string) bool {
	for _, handle := range handles {
		if handle.Name() == name {
			return true
		}
	}
	return false
}

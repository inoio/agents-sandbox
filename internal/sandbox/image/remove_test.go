package image

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestRemoveImagesRefusesReferenced(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	ref := naming.RunnerImageRef(key.Slug, key.Agent)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	err := RemoveImages(context.Background(), []string{ref}, false, &termio.Mock{})
	if err == nil {
		t.Fatal("RemoveImages() error = nil, want a refusal for a referenced image")
	}
	if !strings.Contains(err.Error(), "proj-1mjusbm3wikhb0/opencode") {
		t.Errorf("error %q does not name the referencing project", err.Error())
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none removed", client.RemovedImages)
	}
}

func TestRemoveImagesRefusesImageHeldByLiveSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	handle := &msb.MockSandboxHandle{
		Name_: naming.LegacyVMPrefix + key.Slug + "-" + key.Agent,
		Cfg:   &msbSdk.SandboxConfig{Image: naming.RunnerImageRef(key.Slug, key.Agent)},
	}
	client := &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{handle}}
	msb.WithMsbMock(t, client)

	ref := naming.RunnerImageRef(key.Slug, key.Agent)
	err := RemoveImages(context.Background(), []string{ref}, false, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "proj-1mjusbm3wikhb0/opencode") {
		t.Fatalf("RemoveImages() error = %v, want a refusal naming the live sandbox project", err)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none removed", client.RemovedImages)
	}
}

func TestRemoveImagesForceSkipsGuard(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	ref := naming.RunnerImageRef(key.Slug, key.Agent)
	client := &msb.MockMsbClient{
		ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errors.New("guard must not run under --force")
		},
	}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	if err := RemoveImages(context.Background(), []string{ref}, true, ui); err != nil {
		t.Fatalf("RemoveImages(force): %v", err)
	}
	if len(client.RemovedImages) != 1 || client.RemovedImages[0].Ref != ref || !client.RemovedImages[0].Force {
		t.Fatalf("RemovedImages = %v, want one forced %q", client.RemovedImages, ref)
	}
}

func TestRemoveImagesFailsClosedOnCorruptState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := state.KeyDir(state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"})
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.yaml"), []byte("image_digest: ["), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	if err := RemoveImages(
		context.Background(),
		[]string{naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")},
		false,
		&termio.Mock{},
	); err == nil {
		t.Fatal("RemoveImages() error = nil, want a fail-closed reference scan error")
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none removed", client.RemovedImages)
	}
}

func TestRefuseReferencedImagesNamesProjects(t *testing.T) {
	ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
	references := map[string][]state.Key{
		ref: {
			{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"},
			{Slug: "proj-bCd2345E", Agent: "pi"},
		},
	}

	err := refuseReferencedImages([]string{ref, "unreferenced:latest"}, references)
	if err == nil {
		t.Fatal("refuseReferencedImages() error = nil, want a refusal")
	}
	for _, want := range []string{"proj-1mjusbm3wikhb0/opencode", "proj-bCd2345E/pi"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "unreferenced:latest") {
		t.Errorf("error %q names an unreferenced image", err.Error())
	}
}

func TestRefuseReferencedImagesAllowsUnreferenced(t *testing.T) {
	if err := refuseReferencedImages(
		[]string{naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")},
		map[string][]state.Key{},
	); err != nil {
		t.Fatalf("refuseReferencedImages() error = %v, want nil for unreferenced refs", err)
	}
}

func TestRemoveImagesRefusesNonRunnerRef(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	err := RemoveImages(context.Background(), []string{"docker.io/library/alpine:latest"}, false, ui)
	if err == nil || !strings.Contains(err.Error(), "not managed by agents-sandbox") {
		t.Fatalf("RemoveImages(non-runner) = %v, want an ownership refusal", err)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none removed", client.RemovedImages)
	}
}

func TestRemoveImagesForceDoesNotBypassOwnershipGuard(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	err := RemoveImages(context.Background(), []string{"docker.io/library/alpine:latest"}, true, ui)
	if err == nil || !strings.Contains(err.Error(), "not managed by agents-sandbox") {
		t.Fatalf("RemoveImages(non-runner, force) = %v, want a hard ownership refusal", err)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none removed", client.RemovedImages)
	}
}

// TestRemoveImagesRefusesSingleSegmentRefWithoutHash pins the tightened
// ownership check: a single-segment reference whose slug lacks the launcher's
// 14-character hash is a manually loaded image, not a runner image.
func TestRemoveImagesRefusesSingleSegmentRefWithoutHash(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{
		ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errors.New("the ownership guard must run before the reference scan")
		},
	}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	err := RemoveImages(context.Background(), []string{"manualimage:opencode-latest"}, false, ui)
	if err == nil || !strings.Contains(err.Error(), "not managed by agents-sandbox") {
		t.Fatalf("RemoveImages(hashless single segment) = %v, want an ownership refusal", err)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none removed", client.RemovedImages)
	}
}

// TestRemoveImagesForceDoesNotBypassHashOwnershipGuard pins that the tightened
// guard, like the repository-reference guard, is not overridable by --force.
func TestRemoveImagesForceDoesNotBypassHashOwnershipGuard(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	err := RemoveImages(context.Background(), []string{"manualimage:opencode-latest"}, true, ui)
	if err == nil || !strings.Contains(err.Error(), "not managed by agents-sandbox") {
		t.Fatalf("RemoveImages(hashless single segment, force) = %v, want a hard ownership refusal", err)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none removed", client.RemovedImages)
	}
}

// TestRemoveImagesAcceptsHashBearingRefsEitherScheme pins that a slug carrying
// the launcher hash keeps both reference shapes removable.
func TestRemoveImagesAcceptsHashBearingRefsEitherScheme(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	refs := []string{
		naming.RunnerImageRef("myproj-1mjusbm3wikhb0", "opencode"),
		naming.DockerImageRef("myproj-1mjusbm3wikhb0", "opencode"),
	}
	if err := RemoveImages(context.Background(), refs, false, ui); err != nil {
		t.Fatalf("RemoveImages(hash-bearing refs) = %v, want both removed", err)
	}
	if len(client.RemovedImages) != len(refs) {
		t.Fatalf("RemovedImages = %v, want %d", client.RemovedImages, len(refs))
	}
}

func TestRemoveImagesGuardSurfacesScanError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{
		ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errors.New("list boom")
		},
	}
	msb.WithMsbMock(t, client)

	err := RemoveImages(
		context.Background(),
		[]string{naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")},
		false,
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "check image references") {
		t.Fatalf("RemoveImages(scan error) = %v, want a wrapped scan error", err)
	}
}

func TestInUseImageReferencesKeepsPositionalSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
	handle := &msb.MockSandboxHandle{Name_: "proj-1mjusbm3wikhb0-opencode", Cfg: &msbSdk.SandboxConfig{Image: ref}}
	client := &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{handle}}
	msb.WithMsbMock(t, client)

	references, err := inUseImageReferences(context.Background())
	if err != nil {
		t.Fatalf("inUseImageReferences() = %v", err)
	}
	if len(references[ref]) != 1 {
		t.Fatalf(
			"references[%q] = %v, want one (every sandbox in the private runtime home is ours)",
			ref,
			references[ref],
		)
	}
}

func TestRemoveImagesNonInteractiveRequiresConfirmation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	err := RemoveImages(
		context.Background(),
		[]string{naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")},
		false,
		&termio.Mock{},
	)
	if !errors.Is(err, termio.ErrConfirmationRequired) {
		t.Fatalf("RemoveImages(non-interactive) = %v, want ErrConfirmationRequired", err)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none removed", client.RemovedImages)
	}
}

func TestRemoveImagesSurfacesRemoveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("remove boom")
	client := &msb.MockMsbClient{
		ImageRemoveFn: func(context.Context, string, bool) error { return wantErr },
	}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	err := RemoveImages(
		context.Background(),
		[]string{naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")},
		false,
		ui,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RemoveImages() error = %v, want %v", err, wantErr)
	}
}

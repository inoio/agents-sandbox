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
	key := state.Key{Slug: "proj-aBc1234D", Agent: "opencode"}
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
	if !strings.Contains(err.Error(), "proj-aBc1234D/opencode") {
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
		Name_: naming.VMPrefix + key.Slug + "-" + key.Agent,
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
	key := state.Key{Slug: "proj-aBc1234D", Agent: "opencode"}
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
	dir := state.KeyDir(state.Key{Slug: "proj-aBc1234D", Agent: "opencode"})
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
		[]string{naming.RunnerImageRef("proj-aBc1234D", "opencode")},
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
	ref := naming.RunnerImageRef("proj-aBc1234D", "opencode")
	references := map[string][]state.Key{
		ref: {
			{Slug: "proj-aBc1234D", Agent: "opencode"},
			{Slug: "proj-bCd2345E", Agent: "pi"},
		},
	}

	err := refuseReferencedImages([]string{ref, "unreferenced:latest"}, references)
	if err == nil {
		t.Fatal("refuseReferencedImages() error = nil, want a refusal")
	}
	for _, want := range []string{"proj-aBc1234D/opencode", "proj-bCd2345E/pi"} {
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
		[]string{naming.RunnerImageRef("proj-aBc1234D", "opencode")},
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
		[]string{naming.RunnerImageRef("proj-aBc1234D", "opencode")},
		false,
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "check image references") {
		t.Fatalf("RemoveImages(scan error) = %v, want a wrapped scan error", err)
	}
}

func TestInUseImageReferencesSkipsForeignSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ref := naming.RunnerImageRef("proj-aBc1234D", "opencode")
	foreign := &msb.MockSandboxHandle{Name_: "user-sandbox", Cfg: &msbSdk.SandboxConfig{Image: ref}}
	client := &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{foreign}}
	msb.WithMsbMock(t, client)

	references, err := inUseImageReferences(context.Background())
	if err != nil {
		t.Fatalf("inUseImageReferences() = %v", err)
	}
	if len(references[ref]) != 0 {
		t.Fatalf("references[%q] = %v, want none (a non-launcher sandbox must be skipped)", ref, references[ref])
	}
}

func TestRemoveImagesNonInteractiveRequiresConfirmation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	err := RemoveImages(
		context.Background(),
		[]string{naming.RunnerImageRef("proj-aBc1234D", "opencode")},
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
		[]string{naming.RunnerImageRef("proj-aBc1234D", "opencode")},
		false,
		ui,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RemoveImages() error = %v, want %v", err, wantErr)
	}
}

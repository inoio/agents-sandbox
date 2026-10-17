package volume

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestRemoveVolumesRefusesReferenced(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	name := "agents-sandbox-home-proj-aBc1234D-opencode-20260812T123456"
	if err := seedHomeState(
		state.Key{Slug: "proj-aBc1234D", Agent: "opencode"},
		state.HomeState{HomeVolume: name},
	); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	err := RemoveVolumes(context.Background(), []string{name}, false, &termio.Mock{})
	if err == nil {
		t.Fatal("RemoveVolumes() error = nil, want a refusal for a referenced volume")
	}
	if !strings.Contains(err.Error(), "proj-aBc1234D/opencode") {
		t.Errorf("error %q does not name the referencing project", err.Error())
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none removed", client.RemovedVolumes)
	}
}

func TestRemoveVolumesForceBypassesReference(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	name := "agents-sandbox-home-proj-aBc1234D-opencode-20260812T123456"
	if err := seedHomeState(
		state.Key{Slug: "proj-aBc1234D", Agent: "opencode"},
		state.HomeState{HomeVolume: name},
	); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	if err := RemoveVolumes(context.Background(), []string{name}, true, ui); err != nil {
		t.Fatalf("RemoveVolumes(force): %v", err)
	}
	if len(client.RemovedVolumes) != 1 || client.RemovedVolumes[0] != name {
		t.Fatalf("RemovedVolumes = %v, want [%q]", client.RemovedVolumes, name)
	}
}

func TestRemoveVolumesRemovesSeveral(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	names := []string{"agents-sandbox-home-a-20260812T123456", "agents-sandbox-home-b-20260812T123457"}
	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	if err := RemoveVolumes(context.Background(), names, false, ui); err != nil {
		t.Fatalf("RemoveVolumes: %v", err)
	}
	if len(client.RemovedVolumes) != 2 {
		t.Fatalf("RemovedVolumes = %v, want both", client.RemovedVolumes)
	}
}

func TestRemoveVolumesPropagatesRemoveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("remove boom")
	client := &msb.MockMsbClient{
		RemoveVolumeFn: func(_ context.Context, _ string) error { return wantErr },
	}
	msb.WithMsbMock(t, client)

	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	if err := RemoveVolumes(
		context.Background(),
		[]string{"agents-sandbox-home-a-20260812T123456"},
		false,
		ui,
	); !errors.Is(
		err, wantErr,
	) {
		t.Fatalf("RemoveVolumes() error = %v, want %v", err, wantErr)
	}
}

func TestRemoveVolumesFailsClosedOnReferenceScanError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := state.KeyDir(state.Key{Slug: "proj-aBc1234D", Agent: "opencode"})
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.yaml"), []byte("home_volume: ["), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	if err := RemoveVolumes(
		context.Background(),
		[]string{"agents-sandbox-home-a-20260812T123456"},
		false,
		&termio.Mock{},
	); err == nil {
		t.Fatal("RemoveVolumes() error = nil, want a fail-closed reference scan error")
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none removed", client.RemovedVolumes)
	}
}

func TestRemoveVolumesNonInteractiveRequiresConfirmation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	err := RemoveVolumes(
		context.Background(),
		[]string{"agents-sandbox-home-a-20260812T123456"},
		false,
		&termio.Mock{},
	)
	if !errors.Is(err, termio.ErrConfirmationRequired) {
		t.Fatalf("RemoveVolumes(non-interactive) = %v, want ErrConfirmationRequired", err)
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none removed", client.RemovedVolumes)
	}
}

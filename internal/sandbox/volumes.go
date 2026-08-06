package sandbox

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Action constants for the home volume resolution prompt.
const (
	actionKeep    = "1"
	actionMigrate = "2"
	actionReset   = "3"
	actionQuit    = "4"
)

func HomeVolumeName(projectSlug string, digest string) string { //nolint:revive // digest retained for API compatibility
	ts := time.Now().UTC().Format("20060102T150405")
	return homePrefix + projectSlug + "-" + ts
}

type VolumeManager struct {
	ui termio.UI
}

func NewVolumeManager(ui termio.UI) *VolumeManager {
	return &VolumeManager{ui: ui}
}

func (vm *VolumeManager) EnsureHome(
	ctx context.Context,
	projectSlug, imageDigest, imageTag string,
	opts RunOptions,
	ui termio.UI,
) (string, error) {
	client := msb.Get()
	name := HomeVolumeName(projectSlug, imageDigest)

	_, err := client.GetVolume(ctx, name)
	if err == nil {
		return name, nil
	}

	vol, err := client.CreateVolume(ctx, name,
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create volume %s: %w", name, err)
	}

	if !opts.DryRunVM {
		if err := vm.prefillVolume(ctx, client, projectSlug, vol.Name(), imageTag, ui); err != nil {
			return "", err
		}
	} else {
		vm.ui.Infof("dry-run: Would prefill home volume")
	}

	return name, nil
}

func (vm *VolumeManager) prefillVolume(
	ctx context.Context,
	client MsbClient,
	projectSlug, volumeName, imageTag string,
	ui termio.UI,
) error {
	prefillName := taskPrefix + projectSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	mountConfig := msbSdk.Mount.Named(volumeName, msbSdk.MountOptions{})

	spin := ui.Spinner("Preparing home volume")
	sb, err := client.CreateSandbox(ctx, prefillName,
		msbSdk.WithImage(imageTag),
		msbSdk.WithMounts(map[string]msbSdk.MountConfig{
			"/mnt/home": mountConfig,
		}),
		msbSdk.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("create prefill sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = client.RemoveSandbox(context.Background(), prefillName)
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c", "cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home"})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("prefill cp: %w", err)
	}
	if !out.Success() {
		err := fmt.Errorf("prefill cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return err
	}
	spin.Stop()
	return nil
}

// ResolveHomeVolume checks the state file for an existing volume reference.
// If found and the volume still exists, returns the volume name and state.
// If not found or the volume does not exist, falls through to ensureNewHome.
func (vm *VolumeManager) ResolveHomeVolume(
	ctx context.Context,
	client MsbClient,
	projectSlug, imageDigest, imageTag string,
	opts RunOptions,
	ui termio.UI,
) (string, HomeState, error) {
	state, err := ReadState(projectSlug)
	if err != nil {
		ui.Warnf("corrupted state file, creating fresh home volume")
		return vm.ensureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui)
	}

	if state == nil {
		return vm.ensureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui)
	}

	_, err = client.GetVolume(ctx, state.HomeVolume)
	if err != nil {
		ui.Warnf("existing home volume %q not found, creating fresh", state.HomeVolume)
		return vm.ensureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui)
	}

	return state.HomeVolume, *state, nil
}

// ensureNewHome creates a fresh home volume from the image and writes the state.
func (vm *VolumeManager) ensureNewHome(
	ctx context.Context,
	client MsbClient,
	projectSlug, imageDigest, imageTag string,
	opts RunOptions,
	ui termio.UI,
) (string, HomeState, error) {
	volName := HomeVolumeName(projectSlug, "")
	vol, err := client.CreateVolume(ctx, volName,
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return "", HomeState{}, fmt.Errorf("create volume %s: %w", volName, err)
	}

	if !opts.DryRunVM {
		if err := vm.prefillVolume(ctx, client, projectSlug, vol.Name(), imageTag, ui); err != nil {
			return "", HomeState{}, err
		}
	} else {
		ui.Infof("dry-run: Would prefill home volume")
	}

	state := HomeState{
		HomeVolume:  volName,
		ImageDigest: imageDigest,
	}
	if err := WriteState(projectSlug, state); err != nil {
		ui.Warnf("failed to write state file: %v", err)
	}
	return volName, state, nil
}

// ResolveHomeAction compares the stored image digest with the current one.
// If they match, returns actionKeep immediately.
// If they differ, presents a prompt: keep/migrate/reset/quit.
// In non-interactive mode or with --yes, defaults to actionKeep.
func (vm *VolumeManager) ResolveHomeAction(
	ui termio.UI,
	storedDigest, currentDigest string,
) string {
	if storedDigest == currentDigest {
		return actionKeep
	}

	if !ui.IsInteractive() {
		ui.Infof("non-interactive: using existing home volume")
		return actionKeep
	}

	prompt := "Docker image changed for project. The image's home directory is different from your current one."
	choices := []termio.Choice{
		{Key: actionKeep, Label: "keep", Description: "continue with existing home volume"},
		{Key: actionMigrate, Label: "migrate", Description: "create fresh volume, copy all files on top"},
		{Key: actionReset, Label: "reset", Description: "replace with fresh volume from image (lose local changes)"},
		{Key: actionQuit, Label: "quit", Description: "exit without starting a session"},
	}
	selected, err := ui.Select(prompt, choices, actionKeep)
	if err != nil {
		ui.Warnf("prompt failed, continuing with existing volume")
		return actionKeep
	}
	return selected
}

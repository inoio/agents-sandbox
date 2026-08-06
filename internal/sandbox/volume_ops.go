package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// checkForActiveVMs checks if there are active or stopped VMs for the given slug.
func checkForActiveVMs(ctx context.Context, slug string) error {
	client := msb.Get()
	sandboxes, err := client.ListSandboxes(ctx)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	for _, h := range sandboxes {
		if !strings.HasPrefix(h.Name(), vmPrefix) {
			continue
		}
		s, _ := extractProjectSlugAndDigest(h.Name())
		if s == slug {
			status := h.Status()
			if isSandboxActive(status) || isStoppedStatus(status) {
				return fmt.Errorf(
					"session still running for slug %q -- quit all sessions before migrating or resetting",
					slug,
				)
			}
		}
	}
	return nil
}

// CmdMigrate creates a new home volume, copies old files on top, updates state.
func CmdMigrate(
	ctx context.Context,
	projectSlug, volumeName, imageTag string,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui, true, false)
}

// CmdReset creates a new home volume from image only, no copy.
func CmdReset(
	ctx context.Context,
	projectSlug, volumeName, imageTag string,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui, false, false)
}

// CmdEdit creates a new volume alongside the old for manual transfer.
func CmdEdit(
	ctx context.Context,
	projectSlug, volumeName, imageTag string,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui, false, true)
}

// volumeOp is the shared implementation for migrate, reset, and edit operations.
//
//nolint:gocognit,gocritic,funlen,nestif // Complex multi-operation flow with shared cleanup logic
func volumeOp(
	ctx context.Context,
	projectSlug, volumeName, imageTag string,
	rmOld, dryRun bool,
	ui termio.UI,
	doCopy, doEdit bool,
) error {
	client := msb.Get()

	state, err := ReadState(projectSlug)
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("no state file found for project %q", projectSlug)
	}

	oldVolume := volumeName
	if oldVolume == "" {
		oldVolume = state.HomeVolume
		if oldVolume == "" {
			return errors.New("no volume to operate on: state has no home_volume set")
		}
	}

	if vmErr := checkForActiveVMs(ctx, projectSlug); vmErr != nil {
		return vmErr
	}

	if dryRun {
		if doCopy {
			ui.Infof("dry-run: Would create volume %q, copy files from %q", HomeVolumeName(projectSlug, ""), oldVolume)
		} else if doEdit {
			ui.Infof(
				"dry-run: Would create volume %q alongside %q for manual transfer",
				HomeVolumeName(projectSlug, ""),
				oldVolume,
			)
		} else {
			ui.Infof("dry-run: Would create fresh volume %q, remove %q", HomeVolumeName(projectSlug, ""), oldVolume)
		}
		return nil
	}

	newVolumeName := HomeVolumeName(projectSlug, "")
	newVol, err := client.CreateVolume(ctx, newVolumeName,
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return fmt.Errorf("create volume %s: %w", newVolumeName, err)
	}

	vm := NewVolumeManager(ui)
	if err := vm.prefillVolume(ctx, client, projectSlug, newVol.Name(), imageTag, ui); err != nil {
		return fmt.Errorf("prefill new volume: %w", err)
	}

	if doCopy {
		copySbName := taskPrefix + projectSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		copySb, copyErr := client.CreateSandbox(ctx, copySbName,
			msbSdk.WithImage(imageTag),
			msbSdk.WithMounts(map[string]msbSdk.MountConfig{
				"/src": msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{}),
				"/dst": msbSdk.Mount.Named(newVolumeName, msbSdk.MountOptions{}),
			}),
			msbSdk.WithReplace(),
		)
		if copyErr != nil {
			return fmt.Errorf("create copy sandbox: %w", copyErr)
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
			defer cancel()
			_ = copySb.Detach(stopCtx)
			_ = copySb.Close()
			_ = client.RemoveSandbox(context.Background(), copySbName)
		}()

		spin := ui.Spinner("Copying files from existing home volume")
		copyOut, copyExecErr := copySb.Exec(ctx, "sh", []string{"-c", "cp -a /src/. /dst/ && chown -R dev:dev /dst"})
		if copyExecErr != nil {
			spin.StopError(copyExecErr)
			return fmt.Errorf("copy files: %w", copyExecErr)
		}
		if copyOut != nil && !copyOut.Success() {
			spin.StopError(fmt.Errorf("copy failed (exit %d): %s", copyOut.ExitCode(), copyOut.Stderr()))
			return fmt.Errorf("copy files (exit %d): %s", copyOut.ExitCode(), copyOut.Stderr())
		}
		spin.Stop()
		ui.Infof("migrated to new home volume %q (files copied from %q)", newVolumeName, oldVolume)
	} else if doEdit {
		// Spawn an interactive shell in a sandbox that has both volumes mounted.
		spin := ui.Spinner("Starting interactive session with both volumes")
		editSandboxName := taskPrefix + projectSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		editOldMount := msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{})
		editNewMount := msbSdk.Mount.Named(newVolumeName, msbSdk.MountOptions{})
		editSb, editErr := client.CreateSandbox(ctx, editSandboxName,
			msbSdk.WithImage(imageTag),
			msbSdk.WithMounts(map[string]msbSdk.MountConfig{
				"/src": editOldMount,
				"/dst": editNewMount,
			}),
			msbSdk.WithReplace(),
		)
		if editErr != nil {
			return fmt.Errorf("create edit sandbox: %w", editErr)
		}
		ui.Infof("Both volumes mounted in session:")
		ui.Infof("  Old volume (source):  /src")
		ui.Infof("  New volume (target):  /dst")
		ui.Infof("Type 'exit' to finish and return to the host.")
		spin.Stop()
		_, shellErr := editSb.Attach(ctx, "/bin/bash", "-l")
		if shellErr != nil {
			ui.Warnf("shell exited with error: %v", shellErr)
		}
		// Best-effort cleanup
		stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
		defer cancel()
		_ = editSb.Stop(stopCtx)
		_ = editSb.Close()
		_ = client.RemoveSandbox(context.Background(), editSandboxName)
		ui.Infof("exited interactive session, new volume: %q", newVolumeName)
	} else {
		ui.Infof("reset to new home volume %q", newVolumeName)
	}

	newState := HomeState{
		HomeVolume:  newVolumeName,
		ImageDigest: state.ImageDigest,
	}
	if err := WriteState(projectSlug, newState); err != nil {
		ui.Warnf("failed to write state file: %v", err)
	}

	if rmOld {
		if err := client.RemoveVolume(ctx, oldVolume); err != nil {
			ui.Warnf("failed to remove old volume %q: %v", oldVolume, err)
		} else {
			ui.Infof("removed old volume %q", oldVolume)
		}
	}

	return nil
}

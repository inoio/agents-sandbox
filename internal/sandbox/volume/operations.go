package volume

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// checkForActiveVMs checks if there are active VMs for the given slug.
func checkForActiveVMs(ctx context.Context, slug string) error {
	client := msb.Get()
	sandboxes, err := client.ListSandboxes(ctx, nil)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	for _, handle := range sandboxes {
		if !strings.HasPrefix(handle.Name(), naming.VmPrefix) {
			continue
		}
		if naming.ArtifactFor(handle.Name()).Slug == slug {
			status := handle.Status()
			if msb.IsSandboxActive(status) {
				return fmt.Errorf(
					"VM still running for slug %q -- quit all sessions before migrating or resetting",
					slug,
				)
			}
		}
	}
	return nil
}

// volDryRunFunc returns the dry-run message string for the given operation.
// It receives the resolved source volume; the project slug and other context
// are captured by the enclosing command's closure.
type volDryRunFunc func(oldVolume string) string

// volMainFunc implements the operation's main behavior after volume
// preflight (state read, active-VM check, volume creation, prefill). It
// receives the resolved source volume and the freshly created target volume;
// the project slug, image tag, and UI are captured by the enclosing command's
// closure, and clients are obtained via msb.Get() when needed.
type volMainFunc func(oldVolume, newVolumeName string) error

// volCallbacks contains the callbacks for each volume operation variant.
type volCallbacks struct {
	dryRun volDryRunFunc
	main   volMainFunc
}

// CmdMigrate creates a new home volume, copies old files on top, updates state.
func CmdMigrate(
	ctx context.Context,
	projectSlug, volumeName, imageTag, currentDigest string,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, projectSlug, volumeName, imageTag, currentDigest, rmOld, dryRun, volCallbacks{
		dryRun: func(oldVolume string) string {
			return fmt.Sprintf(
				"dry-run: Would create volume %q, copy files from %q",
				HomeVolumeName(projectSlug),
				oldVolume,
			)
		},
		main: func(oldVolume, newVolumeName string) error {
			if err := NewManager(ui).CopyVolume(
				ctx, msb.Get(), projectSlug, oldVolume, newVolumeName, imageTag, ui,
			); err != nil {
				return err
			}
			ui.Infof("migrated to new home volume %q (files copied from %q)", newVolumeName, oldVolume)
			return nil
		},
	}, ui)
}

// CmdReset creates a new home volume from image only, no copy.
func CmdReset(
	ctx context.Context,
	projectSlug, volumeName, imageTag, currentDigest string,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, projectSlug, volumeName, imageTag, currentDigest, rmOld, dryRun, volCallbacks{
		dryRun: func(oldVolume string) string {
			return fmt.Sprintf(
				"dry-run: Would create fresh volume %q, remove %q",
				HomeVolumeName(projectSlug),
				oldVolume,
			)
		},
		main: func(_ string, newVolumeName string) error {
			ui.Infof("reset to new home volume %q", newVolumeName)
			return nil
		},
	}, ui)
}

// CmdEdit creates a new volume alongside the old for manual transfer.
func CmdEdit(
	ctx context.Context,
	projectSlug, volumeName, imageTag, currentDigest string,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, projectSlug, volumeName, imageTag, currentDigest, rmOld, dryRun, volCallbacks{
		dryRun: func(oldVolume string) string {
			return fmt.Sprintf(
				"dry-run: Would create volume %q alongside %q for manual transfer",
				HomeVolumeName(projectSlug),
				oldVolume,
			)
		},
		main: func(oldVolume, newVolumeName string) error {
			client := msb.Get()
			spin := ui.Spinner("Starting interactive session with both volumes")
			editSandboxName := naming.TaskPrefix + projectSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			editOldMount := msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{})
			editNewMount := msbSdk.Mount.Named(newVolumeName, msbSdk.MountOptions{})
			editSb, editErr := client.CreateSandbox(ctx, editSandboxName,
				msbSdk.WithImage(imageTag),
				msbSdk.WithMounts(map[string]msbSdk.MountConfig{
					srcMount: editOldMount,
					dstMount: editNewMount,
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
			stopCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
			_ = editSb.Stop(stopCtx)
			cancel()
			_ = editSb.Close()
			_ = client.RemoveSandbox(context.Background(), editSandboxName)
			ui.Infof("exited interactive session, new volume: %q", newVolumeName)
			return nil
		},
	}, ui)
}

// volumeOp is the shared implementation for migrate, reset, and edit operations.
func volumeOp(
	ctx context.Context,
	projectSlug, volumeName, imageTag, currentDigest string,
	rmOld, dryRun bool,
	cbs volCallbacks,
	ui termio.UI,
) error {
	client := msb.Get()

	st, err := state.ReadState(projectSlug)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return fmt.Errorf("no state file found for project %q", projectSlug)
		}
		return fmt.Errorf("read state: %w", err)
	}

	oldVolume := volumeName
	if oldVolume == "" {
		oldVolume = st.HomeVolume
		if oldVolume == "" {
			return errors.New("no volume to operate on: state has no home_volume set")
		}
	}

	if vmErr := checkForActiveVMs(ctx, projectSlug); vmErr != nil {
		return vmErr
	}

	if dryRun {
		ui.Info(cbs.dryRun(oldVolume))
		return nil
	}

	newVolumeName := HomeVolumeName(projectSlug)
	newVol, err := client.CreateVolume(ctx, newVolumeName,
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return fmt.Errorf("create volume %s: %w", newVolumeName, err)
	}

	vm := NewManager(ui)
	if err := vm.PrefillVolume(ctx, client, projectSlug, newVol.Name(), imageTag, ui); err != nil {
		return fmt.Errorf("prefill new volume: %w", err)
	}

	if err := cbs.main(oldVolume, newVolumeName); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
		defer cancel()
		if rmErr := client.RemoveVolume(cleanupCtx, newVolumeName); rmErr != nil {
			ui.Warnf("failed to clean up new volume %q after error: %v", newVolumeName, rmErr)
		}
		return err
	}

	// Preserve the env/secret fingerprints from the previous state: a volume
	// operation changes the home volume data, not the env/secrets baked into
	// the VM, so discarding them would trigger a false VM rebuild on the next
	// startup (see EnvChanged/SecretsChanged treating empty state as "changed").
	newState := state.NewHomeState(newVolumeName, currentDigest)
	newState.EnvState = st.EnvState
	newState.SecretState = st.SecretState
	if err := state.WriteState(projectSlug, newState); err != nil {
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

package sandbox

import (
	"context"
	"fmt"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func HomeVolumeName(projectSlug, imageDigest string) string {
	return "opencode-msb-home-" + projectSlug + "-" + git.HashID(imageDigest)
}

type VolumeManager struct {
	ui stdio.UI
}

func NewVolumeManager(ui stdio.UI) *VolumeManager {
	return &VolumeManager{ui: ui}
}

func (vm *VolumeManager) EnsureHome(
	ctx context.Context,
	projectSlug, imageDigest, imageTag string,
	opts RunOptions,
	ui stdio.UI,
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
	ui stdio.UI,
) error {
	prefillName := fmt.Sprintf("opencode-msb-task-prefill-%s-%d", projectSlug, time.Now().UnixNano())
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

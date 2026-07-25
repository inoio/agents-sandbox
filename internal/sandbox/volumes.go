package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func sanitizeDigest(digest string) string {
	return strings.ReplaceAll(digest, ":", "-")
}

func HomeVolumeName(projectSlug, imageDigest string) string {
	return projectSlug + "-opencode-home-" + sanitizeDigest(imageDigest)
}

type VolumeManager struct {
	logger *log.Logger
}

func NewVolumeManager(logger *log.Logger) *VolumeManager {
	return &VolumeManager{logger: logger}
}

func (vm *VolumeManager) EnsureHome(
	ctx context.Context,
	projectSlug, imageDigest, imageTag string,
) (string, error) {
	name := HomeVolumeName(projectSlug, imageDigest)

	_, err := msb.GetVolume(ctx, name)
	if err == nil {
		return name, nil
	}

	vol, err := msb.CreateVolume(ctx, name,
		msb.WithVolumeKind(msb.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create volume %s: %w", name, err)
	}

	if err := vm.prefillVolume(ctx, vol.Name(), imageTag); err != nil {
		return "", fmt.Errorf("prefill volume %s: %w", name, err)
	}

	return name, nil
}

func (vm *VolumeManager) prefillVolume(ctx context.Context, volumeName, imageTag string) error {
	prefillName := fmt.Sprintf("opencode-msb-prefill-%d", time.Now().UnixNano())

	mountConfig := msb.Mount.Named(volumeName, msb.MountOptions{})

	spin := log.NewSpinner(vm.logger)
	spin.Start("Preparing home volume")
	sb, err := msb.CreateSandbox(ctx, prefillName,
		msb.WithImage(imageTag),
		msb.WithMounts(map[string]msb.MountConfig{
			"/mnt/home": mountConfig,
		}),
		msb.WithReplace(),
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
		_ = msb.RemoveSandbox(context.Background(), prefillName)
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

type rawMountSpec struct {
	Named string `json:"named,omitempty"`
}

type rawSandboxConfig struct {
	Volumes map[string]rawMountSpec `json:"volumes,omitempty"`
}

func extractNamedVolumes(configJSON string) []string {
	var raw rawSandboxConfig
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return nil
	}
	var names []string
	for _, spec := range raw.Volumes {
		if spec.Named != "" {
			names = append(names, spec.Named)
		}
	}
	return names
}

func cloneVolumeName(sourceVol string) string {
	return fmt.Sprintf("%s-clone-%d", sourceVol, time.Now().UnixNano())
}

func (vm *VolumeManager) CloneVolume(
	ctx context.Context,
	sourceVol, imageTag string,
) (string, error) {
	cloneName := cloneVolumeName(sourceVol)

	vol, err := msb.CreateVolume(ctx, cloneName,
		msb.WithVolumeKind(msb.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create clone volume %s: %w", cloneName, err)
	}

	// Clean up the clone volume if any subsequent step fails.
	defer func() {
		if err != nil {
			_ = msb.RemoveVolume(context.Background(), cloneName)
		}
	}()

	prefillName := fmt.Sprintf("opencode-msb-clone-%d", time.Now().UnixNano())

	mounts := map[string]msb.MountConfig{
		"/mnt/src": msb.Mount.Named(sourceVol, msb.MountOptions{Readonly: true}),
		"/mnt/dst": msb.Mount.Named(vol.Name(), msb.MountOptions{}),
	}

	spin := log.NewSpinner(vm.logger)
	spin.Start("Cloning home volume")
	sb, err := msb.CreateSandbox(ctx, prefillName,
		msb.WithImage(imageTag),
		msb.WithMounts(mounts),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return "", fmt.Errorf("create clone sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), prefillName)
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c",
		"cp -a /mnt/src/. /mnt/dst/ && chown -R dev:dev /mnt/dst && find /mnt/dst -name '*.shm' -delete",
	})
	if err != nil {
		spin.StopError(err)
		return "", fmt.Errorf("clone cp: %w", err)
	}
	if !out.Success() {
		err = fmt.Errorf("clone cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return "", err
	}
	spin.Stop()
	return cloneName, nil
}

func sameHomeVolumeInUse(
	ctx context.Context,
	volumeName, excludeSandbox string,
) (string, bool, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list sandboxes: %w", err)
	}
	for _, h := range handles {
		if h.Name() == excludeSandbox {
			continue
		}
		if !isSandboxActive(h.Status()) {
			continue
		}
		if slices.Contains(extractNamedVolumes(h.ConfigJSON()), volumeName) {
			return h.Name(), true, nil
		}
	}
	return "", false, nil
}

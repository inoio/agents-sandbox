package sandbox

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// msbClient is the unified abstraction over the microsandbox SDK used by the
// sandbox package. It covers discovery, creation, and deletion of sandboxes,
// volumes, and images, plus runtime setup. Production code uses realMsbClient;
// tests replace newMsbClient to inject mocks.
type msbClient interface {
	EnsureInstalled(ctx context.Context) error

	GetSandbox(ctx context.Context, name string) (msbSandboxHandle, error)
	CreateSandbox(ctx context.Context, name string, opts ...msb.SandboxOption) (msbSandbox, error)
	ListSandboxes(ctx context.Context) ([]msbSandboxHandle, error)
	RemoveSandbox(ctx context.Context, name string) error

	GetVolume(ctx context.Context, name string) (msbVolumeHandle, error)
	CreateVolume(ctx context.Context, name string, opts ...msb.VolumeOption) (msbVolumeHandle, error)
	ListVolumes(ctx context.Context) ([]msbVolumeHandle, error)
	RemoveVolume(ctx context.Context, name string) error

	ImageGet(ctx context.Context, ref string) error
	ImageList(ctx context.Context) ([]msbImageHandle, error)
	ImageRemove(ctx context.Context, ref string, force bool) error
	ImageLoad(ctx context.Context, ref string, r io.Reader) error
}

// msbSandboxHandle is the subset of *msb.SandboxHandle used by the launcher.
type msbSandboxHandle interface {
	Name() string
	Status() msb.SandboxStatus
	UpdatedAt() time.Time
	Image() string

	Connect(ctx context.Context) (msbSandbox, error)
	Refresh(ctx context.Context) (msbSandboxHandle, error)
	Start(ctx context.Context) (msbSandbox, error)
	Stop(ctx context.Context, opts ...msb.StopOption) error
	Kill(ctx context.Context, opts ...msb.KillOption) error
	Remove(ctx context.Context) error
}

// msbSandbox is the subset of *msb.Sandbox used by the launcher.
type msbSandbox interface {
	FS() sandboxFS
	Fs() fsLister
	Shell(ctx context.Context, command string, opts ...msb.ExecOption) (shellResult, error)
	Exec(ctx context.Context, command string, args []string, opts ...msb.ExecOption) (shellResult, error)
	Attach(ctx context.Context, command string, args ...string) (int, error)
	Detach(ctx context.Context) error
	Stop(ctx context.Context, opts ...msb.StopOption) error
	Close() error
}

// shellResult is the subset of the MSB shell/exec result type used by the launcher.
type shellResult interface {
	Success() bool
	ExitCode() int
	Stdout() string
	Stderr() string
	StdoutBytes() []byte
}

// msbVolumeHandle is the subset of *msb.VolumeHandle that the launcher needs.
type msbVolumeHandle interface {
	Name() string
	Path() string
	Kind() msb.VolumeKind
}

// msbImageHandle is the subset of *msb.ImageHandle that the launcher needs.
type msbImageHandle interface {
	Reference() string
	ManifestDigest() string
}

// newMsbClient is the factory the sandbox package uses to obtain an msbClient.
// Tests override it to inject mocks without changing public APIs.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable SDK
var newMsbClient = func() msbClient {
	return &realMsbClient{}
}

// realMsbClient delegates to the actual microsandbox SDK.
type realMsbClient struct{}

func (realMsbClient) EnsureInstalled(ctx context.Context) error {
	return msb.EnsureInstalled(ctx)
}

func (realMsbClient) GetSandbox(ctx context.Context, name string) (msbSandboxHandle, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return nil, err
	}
	return realSandboxHandle{handle: h}, nil
}

func (realMsbClient) CreateSandbox(ctx context.Context, name string, opts ...msb.SandboxOption) (msbSandbox, error) {
	sb, err := msb.CreateSandbox(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	return realSandbox{sandbox: sb}, nil
}

func (realMsbClient) ListSandboxes(ctx context.Context) ([]msbSandboxHandle, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]msbSandboxHandle, len(handles))
	for i, h := range handles {
		result[i] = realSandboxHandle{handle: h}
	}
	return result, nil
}

func (realMsbClient) RemoveSandbox(ctx context.Context, name string) error {
	return msb.RemoveSandbox(ctx, name)
}

func (realMsbClient) GetVolume(ctx context.Context, name string) (msbVolumeHandle, error) {
	return msb.GetVolume(ctx, name)
}

func (realMsbClient) CreateVolume(ctx context.Context, name string, opts ...msb.VolumeOption) (msbVolumeHandle, error) {
	vol, err := msb.CreateVolume(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	return realVolumeHandle{vol: vol}, nil
}

func (realMsbClient) ListVolumes(ctx context.Context) ([]msbVolumeHandle, error) {
	handles, err := msb.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]msbVolumeHandle, len(handles))
	for i, h := range handles {
		result[i] = h
	}
	return result, nil
}

func (realMsbClient) RemoveVolume(ctx context.Context, name string) error {
	return msb.RemoveVolume(ctx, name)
}

func (realMsbClient) ImageGet(ctx context.Context, ref string) error {
	_, err := msb.Image.Get(ctx, ref)
	return err
}

func (realMsbClient) ImageList(ctx context.Context) ([]msbImageHandle, error) {
	handles, err := msb.Image.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]msbImageHandle, len(handles))
	for i, h := range handles {
		result[i] = h
	}
	return result, nil
}

func (realMsbClient) ImageRemove(ctx context.Context, ref string, force bool) error {
	return msb.Image.Remove(ctx, ref, force)
}

func (realMsbClient) ImageLoad(ctx context.Context, ref string, r io.Reader) error {
	cmd := exec.CommandContext(ctx, "msb", "load", "--tag", ref)
	cmd.Stdin = r
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("loading image into microsandbox failed: %w: %s", err, out)
	}
	return nil
}

// realVolumeHandle adapts *msb.Volume to msbVolumeHandle.
type realVolumeHandle struct {
	vol *msb.Volume
}

func (v realVolumeHandle) Name() string {
	return v.vol.Name()
}

func (v realVolumeHandle) Path() string {
	return v.vol.Path()
}

func (v realVolumeHandle) Kind() msb.VolumeKind {
	return msb.VolumeKindDir
}

// realSandboxHandle adapts *msb.SandboxHandle to msbSandboxHandle.
type realSandboxHandle struct {
	handle *msb.SandboxHandle
}

func (w realSandboxHandle) Name() string {
	return w.handle.Name()
}

func (w realSandboxHandle) Status() msb.SandboxStatus {
	return w.handle.Status()
}

func (w realSandboxHandle) UpdatedAt() time.Time {
	return w.handle.UpdatedAt()
}

func (w realSandboxHandle) Image() string {
	cfg, err := w.handle.Config()
	if err != nil {
		return ""
	}
	return cfg.Image
}

func (w realSandboxHandle) Connect(ctx context.Context) (msbSandbox, error) {
	sb, err := w.handle.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return realSandbox{sandbox: sb}, nil
}

func (w realSandboxHandle) Refresh(ctx context.Context) (msbSandboxHandle, error) {
	h, err := w.handle.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	return realSandboxHandle{handle: h}, nil
}

func (w realSandboxHandle) Start(ctx context.Context) (msbSandbox, error) {
	sb, err := w.handle.Start(ctx)
	if err != nil {
		return nil, err
	}
	return realSandbox{sandbox: sb}, nil
}

func (w realSandboxHandle) Stop(ctx context.Context, opts ...msb.StopOption) error {
	return w.handle.Stop(ctx, opts...)
}

func (w realSandboxHandle) Kill(ctx context.Context, opts ...msb.KillOption) error {
	return w.handle.Kill(ctx, opts...)
}

func (w realSandboxHandle) Remove(ctx context.Context) error {
	return w.handle.Remove(ctx)
}

// realSandbox adapts *msb.Sandbox to msbSandbox.
type realSandbox struct {
	sandbox *msb.Sandbox
}

func (s realSandbox) FS() sandboxFS {
	return s.sandbox.FS()
}

func (s realSandbox) Fs() fsLister {
	return s.sandbox.FS()
}

func (s realSandbox) Shell(ctx context.Context, command string, opts ...msb.ExecOption) (shellResult, error) {
	return s.sandbox.Shell(ctx, command, opts...)
}

func (s realSandbox) Exec(
	ctx context.Context,
	command string,
	args []string,
	opts ...msb.ExecOption,
) (shellResult, error) {
	return s.sandbox.Exec(ctx, command, args, opts...)
}

func (s realSandbox) Attach(ctx context.Context, command string, args ...string) (int, error) {
	return s.sandbox.Attach(ctx, command, args...)
}

func (s realSandbox) Detach(ctx context.Context) error {
	return s.sandbox.Detach(ctx)
}

func (s realSandbox) Stop(ctx context.Context, opts ...msb.StopOption) error {
	return s.sandbox.Stop(ctx, opts...)
}

func (s realSandbox) Close() error {
	return s.sandbox.Close()
}

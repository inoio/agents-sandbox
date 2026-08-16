package msb

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Get is the factory clients can use to obtain an Client.
// Tests override Get to inject mocks.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable SDK
var Get = func() Client {
	return &realMsbClient{}
}

// Client is the public abstraction over the microsandbox SDK used by the
// sandbox package. It covers discovery, creation, and deletion of sandboxes,
// volumes, and images, plus runtime setup. Production code uses realMsbClient;
// tests construct MockMsbClient directly.
type Client interface {
	EnsureInstalled(ctx context.Context) error
	GetSandbox(ctx context.Context, name string) (SandboxHandle, error)
	CreateSandbox(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (Sandbox, error)
	ListSandboxes(ctx context.Context) ([]SandboxHandle, error)
	RemoveSandbox(ctx context.Context, name string) error
	GetVolume(ctx context.Context, name string) (VolumeHandle, error)
	CreateVolume(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (VolumeHandle, error)
	ListVolumes(ctx context.Context) ([]VolumeHandle, error)
	RemoveVolume(ctx context.Context, name string) error
	ImageGet(ctx context.Context, ref string) error
	ImageList(ctx context.Context) ([]ImageHandle, error)
	ImageRemove(ctx context.Context, ref string, force bool) error
	ImageLoad(ctx context.Context, ref string, r io.Reader) error
	ImageInspect(ctx context.Context, ref string) (*msbSdk.ImageConfig, error)
}

// SandboxHandle is the subset of *msb.SandboxHandle used by the launcher.
type SandboxHandle interface {
	Name() string
	Status() msbSdk.SandboxStatus
	UpdatedAt() time.Time
	Image() string
	Connect(ctx context.Context) (Sandbox, error)
	Refresh(ctx context.Context) (SandboxHandle, error)
	Start(ctx context.Context) (Sandbox, error)
	Stop(ctx context.Context, opts ...msbSdk.StopOption) error
	Kill(ctx context.Context, opts ...msbSdk.KillOption) error
	Remove(ctx context.Context) error
	Config() (*msbSdk.SandboxConfig, error)
	Modify(ctx context.Context, opts msbSdk.ModifyOptions) (*msbSdk.SandboxModificationPlan, error)
}

// Sandbox is the subset of *msb.Sandbox used by the launcher.
type Sandbox interface {
	FS() SandboxFS
	Shell(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error)
	Exec(ctx context.Context, command string, args []string, opts ...msbSdk.ExecOption) (ShellResult, error)
	AttachWith(ctx context.Context, command string, args []string, opts ...msbSdk.AttachOption) (int, error)
	Attach(ctx context.Context, command string, args ...string) (int, error)
	Detach(ctx context.Context) error
	Stop(ctx context.Context, opts ...msbSdk.StopOption) error
	Close() error
}

// ShellResult is the subset of the MSB shell/exec result type used by the launcher.
type ShellResult interface {
	Success() bool
	ExitCode() int
	Stdout() string
	Stderr() string
	StdoutBytes() []byte
}

// VolumeHandle is the subset of *msb.VolumeHandle that the launcher needs.
type VolumeHandle interface {
	Name() string
	Path() string
	Kind() msbSdk.VolumeKind
	CreatedAt() time.Time
}

// ImageHandle is the subset of *msb.ImageHandle that the launcher needs.
type ImageHandle interface {
	Reference() string
	ManifestDigest() string
	LastUsedAt() time.Time
}

// SandboxFS is the subset of the sandbox filesystem operations used by the launcher.
type SandboxFS interface {
	Exists(ctx context.Context, path string) (bool, error)
	Stat(ctx context.Context, path string) (*msbSdk.FsStat, error)
	List(ctx context.Context, path string) ([]msbSdk.FsEntry, error)
	ReadString(ctx context.Context, path string) (string, error)
	ReadStream(ctx context.Context, path string) (*msbSdk.FsReadStream, error)
	Mkdir(ctx context.Context, path string) error
	Write(ctx context.Context, path string, data []byte) error
	Read(ctx context.Context, path string) ([]byte, error)
	Remove(ctx context.Context, path string) error
}

// VMStatusKind is the semantic lifecycle class of a sandbox status, as
// consumed by the launcher's VM lifecycle decisions.
type VMStatusKind int

const (
	VMStatusUnknown VMStatusKind = iota
	VMStatusActive
	VMStatusStopped
)

// GetVMStatus maps a sandbox status to its lifecycle class. Running, draining,
// and paused are treated as active; stopped and crashed as stopped. Any other
// status is returned as unknown and error.
func GetVMStatus(status msbSdk.SandboxStatus) (VMStatusKind, error) {
	switch status {
	case msbSdk.SandboxStatusRunning, msbSdk.SandboxStatusDraining, msbSdk.SandboxStatusPaused:
		return VMStatusActive, nil
	case msbSdk.SandboxStatusStopped, msbSdk.SandboxStatusCrashed:
		return VMStatusStopped, nil
	default:
		return VMStatusUnknown, fmt.Errorf("unexpected sandbox status: %q", status)
	}
}

// IsSandboxActive reports whether a sandbox status represents a live VM.
func IsSandboxActive(status msbSdk.SandboxStatus) bool {
	kind, err := GetVMStatus(status)
	return err == nil && kind == VMStatusActive
}

// IsNotFound reports whether err is the microsandbox "sandbox does not exist"
// error, unwrapping any wrapped errors.
func IsNotFound(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrSandboxNotFound)
}

// realMsbClient delegates to the actual microsandbox SDK.
type realMsbClient struct{}

func (realMsbClient) EnsureInstalled(ctx context.Context) error {
	return msbSdk.EnsureInstalled(ctx)
}

func (realMsbClient) GetSandbox(ctx context.Context, name string) (SandboxHandle, error) {
	h, err := msbSdk.GetSandbox(ctx, name)
	if err != nil {
		return nil, err
	}
	return &realSandboxHandle{handle: h}, nil
}

func (realMsbClient) CreateSandbox(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (Sandbox, error) {
	sb, err := msbSdk.CreateSandbox(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	return &realSandbox{sandbox: sb}, nil
}

func (realMsbClient) ListSandboxes(ctx context.Context) ([]SandboxHandle, error) {
	var result []SandboxHandle
	var cursor *string
	for {
		var page *msbSdk.SandboxPage
		var err error
		if cursor == nil {
			page, err = msbSdk.ListSandboxes(ctx)
		} else {
			page, err = msbSdk.ListSandboxesWith(ctx, msbSdk.WithListCursor(*cursor))
		}
		if err != nil {
			return nil, err
		}
		for _, h := range page.Sandboxes {
			result = append(result, &realSandboxHandle{handle: h})
		}
		cursor = page.NextCursor
		if cursor == nil {
			break
		}
	}
	return result, nil
}

func (realMsbClient) RemoveSandbox(ctx context.Context, name string) error {
	return msbSdk.RemoveSandbox(ctx, name)
}

func (realMsbClient) GetVolume(ctx context.Context, name string) (VolumeHandle, error) {
	v, err := msbSdk.GetVolume(ctx, name)
	if err != nil {
		return nil, err
	}
	return &realVolumeHandle{val: v}, nil
}

func (realMsbClient) CreateVolume(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (VolumeHandle, error) {
	vol, err := msbSdk.CreateVolume(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	return &realVolumeHandle{val: vol}, nil
}

func (realMsbClient) ListVolumes(ctx context.Context) ([]VolumeHandle, error) {
	handles, err := msbSdk.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]VolumeHandle, len(handles))
	for i, h := range handles {
		result[i] = &realVolumeHandle{val: h}
	}
	return result, nil
}

func (realMsbClient) RemoveVolume(ctx context.Context, name string) error {
	return msbSdk.RemoveVolume(ctx, name)
}

func (realMsbClient) ImageGet(ctx context.Context, ref string) error {
	_, err := msbSdk.Image.Get(ctx, ref)
	return err
}

func (realMsbClient) ImageList(ctx context.Context) ([]ImageHandle, error) {
	handles, err := msbSdk.Image.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ImageHandle, len(handles))
	for i, h := range handles {
		result[i] = h
	}
	return result, nil
}

func (realMsbClient) ImageRemove(ctx context.Context, ref string, force bool) error {
	return msbSdk.Image.Remove(ctx, ref, force)
}

func (realMsbClient) ImageLoad(ctx context.Context, ref string, r io.Reader) error {
	cmd := exec.CommandContext(ctx, "msb", "load", "--tag", ref)
	cmd.Stdin = r
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("loading image into microsandbox failed: %w: %s", err, out)
	}
	return nil
}

func (realMsbClient) ImageInspect(ctx context.Context, ref string) (*msbSdk.ImageConfig, error) {
	detail, err := msbSdk.Image.Inspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	return detail.Config, nil
}

// realVolumeHandle adapts *msbSdk.Volume or *msbSdk.VolumeHandle to VolumeHandle.
type realVolumeHandle struct {
	val any
}

func (v realVolumeHandle) Name() string {
	switch t := v.val.(type) {
	case *msbSdk.VolumeHandle:
		return t.Name()
	case *msbSdk.Volume:
		return t.Name()
	}
	return ""
}

func (v realVolumeHandle) Path() string {
	switch t := v.val.(type) {
	case *msbSdk.VolumeHandle:
		return t.Path()
	case *msbSdk.Volume:
		return t.Path()
	}
	return ""
}

func (v realVolumeHandle) Kind() msbSdk.VolumeKind {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.Kind()
	}
	return msbSdk.VolumeKindDir
}

func (v realVolumeHandle) CreatedAt() time.Time {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.CreatedAt()
	}
	return time.Time{}
}

// realSandboxHandle adapts *msbSdk.SandboxHandle to SandboxHandle.
type realSandboxHandle struct {
	handle *msbSdk.SandboxHandle
}

func (w realSandboxHandle) Name() string {
	return w.handle.Name()
}

func (w realSandboxHandle) Status() msbSdk.SandboxStatus {
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

func (w realSandboxHandle) Connect(ctx context.Context) (Sandbox, error) {
	sb, err := w.handle.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &realSandbox{sandbox: sb}, nil
}

func (w realSandboxHandle) Refresh(ctx context.Context) (SandboxHandle, error) {
	h, err := w.handle.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	return realSandboxHandle{handle: h}, nil
}

func (w realSandboxHandle) Start(ctx context.Context) (Sandbox, error) {
	sb, err := w.handle.Start(ctx)
	if err != nil {
		return nil, err
	}
	return realSandbox{sandbox: sb}, nil
}

func (w realSandboxHandle) Stop(ctx context.Context, opts ...msbSdk.StopOption) error {
	return w.handle.Stop(ctx, opts...)
}

func (w realSandboxHandle) Kill(ctx context.Context, opts ...msbSdk.KillOption) error {
	return w.handle.Kill(ctx, opts...)
}

func (w realSandboxHandle) Remove(ctx context.Context) error {
	return w.handle.Remove(ctx)
}

func (w realSandboxHandle) Config() (*msbSdk.SandboxConfig, error) {
	return w.handle.Config()
}

func (w realSandboxHandle) Modify(
	ctx context.Context,
	opts msbSdk.ModifyOptions,
) (*msbSdk.SandboxModificationPlan, error) {
	return w.handle.Modify(ctx, opts)
}

// realSandbox adapts *msbSdk.Sandbox to Sandbox.
type realSandbox struct {
	sandbox *msbSdk.Sandbox
}

func (s realSandbox) FS() SandboxFS {
	return s.sandbox.FS()
}

func (s realSandbox) Shell(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error) {
	return s.sandbox.Shell(ctx, command, opts...)
}

func (s realSandbox) Exec(
	ctx context.Context,
	command string,
	args []string,
	opts ...msbSdk.ExecOption,
) (ShellResult, error) {
	return s.sandbox.Exec(ctx, command, args, opts...)
}

func (s realSandbox) Attach(ctx context.Context, command string, args ...string) (int, error) {
	return s.sandbox.Attach(ctx, command, args...)
}

func (s realSandbox) AttachWith(
	ctx context.Context,
	command string,
	args []string,
	opts ...msbSdk.AttachOption,
) (int, error) {
	return s.sandbox.AttachWith(ctx, command, args, opts...)
}

func (s realSandbox) Detach(ctx context.Context) error {
	return s.sandbox.Detach(ctx)
}

func (s realSandbox) Stop(ctx context.Context, opts ...msbSdk.StopOption) error {
	return s.sandbox.Stop(ctx, opts...)
}

func (s realSandbox) Close() error {
	return s.sandbox.Close()
}

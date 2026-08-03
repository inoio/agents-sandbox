package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// msbClient is the unified abstraction over the microsandbox SDK used by the
// sandbox package. It covers discovery, creation, and deletion of sandboxes,
// volumes, and images, plus runtime setup. Production code uses realMsbClient;
// tests replace NewMsbClient to inject mocks.
type msbClient = MsbClient

// Deprecated: use SandboxHandle.

type msbSandboxHandle = SandboxHandle

// Deprecated: use Sandbox.

type msbSandbox = Sandbox

// Deprecated: use ShellResult.

type shellResult = ShellResult

// Deprecated: use VolumeHandle.

type msbVolumeHandle = VolumeHandle

// Deprecated: use ImageHandle.

type msbImageHandle = ImageHandle

// newMsbClient is the factory the sandbox package uses to obtain an MsbClient.
// Tests override NewMsbClient to inject mocks.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable SDK
var newMsbClient = func() MsbClient {
	return &realMsbClient{}
}

// NewMsbClient is an exported alias for tests to override with mocks.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable SDK
var NewMsbClient = newMsbClient

// realMsbClient delegates to the actual microsandbox SDK.
type realMsbClient struct{}

func (realMsbClient) EnsureInstalled(ctx context.Context) error {
	return msb.EnsureInstalled(ctx)
}

func (realMsbClient) GetSandbox(ctx context.Context, name string) (SandboxHandle, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return nil, err
	}
	return &realSandboxHandle{handle: h}, nil
}

func (realMsbClient) CreateSandbox(ctx context.Context, name string, opts ...msb.SandboxOption) (Sandbox, error) {
	sb, err := msb.CreateSandbox(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	return &realSandbox{sandbox: sb}, nil
}

func (realMsbClient) ListSandboxes(ctx context.Context) ([]SandboxHandle, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]SandboxHandle, len(handles))
	for i, h := range handles {
		result[i] = &realSandboxHandle{handle: h}
	}
	return result, nil
}

func (realMsbClient) RemoveSandbox(ctx context.Context, name string) error {
	return msb.RemoveSandbox(ctx, name)
}

func (realMsbClient) GetVolume(ctx context.Context, name string) (VolumeHandle, error) {
	v, err := msb.GetVolume(ctx, name)
	if err != nil {
		return nil, err
	}
	return &realVolumeHandle{val: v}, nil
}

func (realMsbClient) CreateVolume(ctx context.Context, name string, opts ...msb.VolumeOption) (VolumeHandle, error) {
	vol, err := msb.CreateVolume(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	return &realVolumeHandle{val: vol}, nil
}

func (realMsbClient) ListVolumes(ctx context.Context) ([]VolumeHandle, error) {
	handles, err := msb.ListVolumes(ctx)
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
	return msb.RemoveVolume(ctx, name)
}

func (realMsbClient) ImageGet(ctx context.Context, ref string) error {
	_, err := msb.Image.Get(ctx, ref)
	return err
}

func (realMsbClient) ImageList(ctx context.Context) ([]ImageHandle, error) {
	handles, err := msb.Image.List(ctx)
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

// realVolumeHandle adapts *msb.Volume or *msb.VolumeHandle to VolumeHandle.
type realVolumeHandle struct {
	val any
}

func (v realVolumeHandle) Name() string {
	switch t := v.val.(type) {
	case *msb.Volume:
		return t.Name()
	case *msb.VolumeHandle:
		return t.Name()
	}
	return ""
}

func (v realVolumeHandle) Path() string {
	switch t := v.val.(type) {
	case *msb.Volume:
		return t.Path()
	case *msb.VolumeHandle:
		return t.Path()
	}
	return ""
}

func (v realVolumeHandle) Kind() msb.VolumeKind {
	switch t := v.val.(type) {
	case *msb.Volume:
		return msb.VolumeKindDir
	case *msb.VolumeHandle:
		return t.Kind()
	}
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

func (w realSandboxHandle) Connect(ctx context.Context) (Sandbox, error) {
	sb, err := w.handle.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &realSandbox{sandbox: sb}, nil
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

// MsbClient is the public abstraction over the microsandbox SDK used by the
// sandbox package. It covers discovery, creation, and deletion of sandboxes,
// volumes, and images, plus runtime setup. Production code uses realMsbClient;
// tests construct MockMsbClient directly.
type MsbClient interface {
	EnsureInstalled(ctx context.Context) error

	GetSandbox(ctx context.Context, name string) (SandboxHandle, error)
	CreateSandbox(ctx context.Context, name string, opts ...msb.SandboxOption) (Sandbox, error)
	ListSandboxes(ctx context.Context) ([]SandboxHandle, error)
	RemoveSandbox(ctx context.Context, name string) error

	GetVolume(ctx context.Context, name string) (VolumeHandle, error)
	CreateVolume(ctx context.Context, name string, opts ...msb.VolumeOption) (VolumeHandle, error)
	ListVolumes(ctx context.Context) ([]VolumeHandle, error)
	RemoveVolume(ctx context.Context, name string) error

	ImageGet(ctx context.Context, ref string) error
	ImageList(ctx context.Context) ([]ImageHandle, error)
	ImageRemove(ctx context.Context, ref string, force bool) error
	ImageLoad(ctx context.Context, ref string, r io.Reader) error
}

// SandboxHandle is the subset of *msb.SandboxHandle used by the launcher.
//
//nolint:revive // SandboxHandle is the standard MSB naming convention
type SandboxHandle interface {
	Name() string
	Status() msb.SandboxStatus
	UpdatedAt() time.Time
	Image() string

	Connect(ctx context.Context) (Sandbox, error)
	Refresh(ctx context.Context) (SandboxHandle, error)
	Start(ctx context.Context) (Sandbox, error)
	Stop(ctx context.Context, opts ...msb.StopOption) error
	Kill(ctx context.Context, opts ...msb.KillOption) error
	Remove(ctx context.Context) error
}

// Sandbox is the subset of *msb.Sandbox used by the launcher.
type Sandbox interface {
	FS() sandboxFS
	Fs() fsLister
	Shell(ctx context.Context, command string, opts ...msb.ExecOption) (ShellResult, error)
	Exec(ctx context.Context, command string, args []string, opts ...msb.ExecOption) (ShellResult, error)
	Attach(ctx context.Context, command string, args ...string) (int, error)
	Detach(ctx context.Context) error
	Stop(ctx context.Context, opts ...msb.StopOption) error
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
	Kind() msb.VolumeKind
}

// ImageHandle is the subset of *msb.ImageHandle that the launcher needs.
type ImageHandle interface {
	Reference() string
	ManifestDigest() string
}

// MockCreateSandboxCall tracks a CreateSandbox call made on MockMsbClient.
type MockCreateSandboxCall struct {
	Name string
	Opts []msb.SandboxOption
}

// MockRemoveImageCall tracks an ImageRemove call made on MockMsbClient.
type MockRemoveImageCall struct {
	Ref   string
	Force bool
}

// MockMsbClient is a test double for MsbClient.
type MockMsbClient struct {
	Sandboxes []SandboxHandle
	Volumes   []VolumeHandle
	Images    []ImageHandle

	createdSandboxes []string
	removedSandboxes []string
	removedVolumes   []string
	removedImages    []MockRemoveImageCall

	ensureInstalledErr error
	getSandboxErr      error
	CreateSandboxErr   error
	ListSandboxesErr   error
	GetVolumeErr       error
	createVolumeErr    error
	ListVolumesErr     error
	ListImagesErr      error
	removeSandboxErr   error
	removeVolumeErr    error
	removeImageErr     error
	imageGetErr        error
	imageLoadErr       error

	createdSandbox      Sandbox
	gotSandbox          SandboxHandle
	gotVolume           VolumeHandle
	CreatedSandboxCalls []MockCreateSandboxCall

	// CreatedSandbox is returned by MockMsbClient.CreateSandbox instead of the
	// default MockSandbox{Name_: name}. External test packages use this to
	// configure sandbox behavior (e.g. AttachErr, AttachCode).
	CreatedSandbox Sandbox
}

// EnsureInstalled implements MsbClient.
func (m *MockMsbClient) EnsureInstalled(_ context.Context) error {
	return m.ensureInstalledErr
}

// GetSandbox implements MsbClient.
func (m *MockMsbClient) GetSandbox(_ context.Context, name string) (SandboxHandle, error) {
	if m.getSandboxErr != nil {
		return nil, m.getSandboxErr
	}
	if m.gotSandbox != nil {
		return m.gotSandbox, nil
	}
	return nil, fmt.Errorf("sandbox not found: %s", name)
}

// CreateSandbox implements MsbClient.
func (m *MockMsbClient) CreateSandbox(_ context.Context, name string, opts ...msb.SandboxOption) (Sandbox, error) {
	m.createdSandboxes = append(m.createdSandboxes, name)
	m.CreatedSandboxCalls = append(m.CreatedSandboxCalls, MockCreateSandboxCall{Name: name, Opts: opts})
	if m.CreateSandboxErr != nil {
		return nil, m.CreateSandboxErr
	}
	if m.CreatedSandbox != nil {
		return m.CreatedSandbox, nil
	}
	if m.createdSandbox != nil {
		return m.createdSandbox, nil
	}
	return &MockSandbox{Name_: name}, nil //nolint:exhaustruct // mock fields are intentionally optional
}

// ListSandboxes implements MsbClient.
func (m *MockMsbClient) ListSandboxes(_ context.Context) ([]SandboxHandle, error) {
	if m.ListSandboxesErr != nil {
		return nil, m.ListSandboxesErr
	}
	return m.Sandboxes, nil
}

// RemoveSandbox implements MsbClient.
func (m *MockMsbClient) RemoveSandbox(_ context.Context, name string) error {
	if m.removeSandboxErr != nil {
		return m.removeSandboxErr
	}
	m.removedSandboxes = append(m.removedSandboxes, name)
	return nil
}

// GetVolume implements MsbClient.
func (m *MockMsbClient) GetVolume(_ context.Context, name string) (VolumeHandle, error) {
	if m.GetVolumeErr != nil {
		return nil, m.GetVolumeErr
	}
	if m.gotVolume != nil {
		return m.gotVolume, nil
	}
	return nil, fmt.Errorf("volume not found: %s", name)
}

// CreateVolume implements MsbClient.
func (m *MockMsbClient) CreateVolume(_ context.Context, name string, _ ...msb.VolumeOption) (VolumeHandle, error) {
	if m.createVolumeErr != nil {
		return nil, m.createVolumeErr
	}
	return &MockVolumeHandle{Name_: name}, nil //nolint:exhaustruct // mock fields are intentionally optional
}

// ListVolumes implements MsbClient.
func (m *MockMsbClient) ListVolumes(_ context.Context) ([]VolumeHandle, error) {
	if m.ListVolumesErr != nil {
		return nil, m.ListVolumesErr
	}
	return m.Volumes, nil
}

// RemoveVolume implements MsbClient.
func (m *MockMsbClient) RemoveVolume(_ context.Context, name string) error {
	if m.removeVolumeErr != nil {
		return m.removeVolumeErr
	}
	m.removedVolumes = append(m.removedVolumes, name)
	return nil
}

// ImageGet implements MsbClient.
func (m *MockMsbClient) ImageGet(_ context.Context, _ string) error {
	return m.imageGetErr
}

// ImageList implements MsbClient.
func (m *MockMsbClient) ImageList(_ context.Context) ([]ImageHandle, error) {
	if m.ListImagesErr != nil {
		return nil, m.ListImagesErr
	}
	return m.Images, nil
}

// ImageRemove implements MsbClient.
func (m *MockMsbClient) ImageRemove(_ context.Context, ref string, force bool) error {
	if m.removeImageErr != nil {
		return m.removeImageErr
	}
	m.removedImages = append(m.removedImages, MockRemoveImageCall{Ref: ref, Force: force})
	return nil
}

// ImageLoad implements MsbClient.
func (m *MockMsbClient) ImageLoad(_ context.Context, ref string, _ io.Reader) error {
	_ = ref
	return m.imageLoadErr
}

// SetGetSandboxErr sets the error returned by MockMsbClient.GetSandbox.
func (m *MockMsbClient) SetGetSandboxErr(err error) {
	m.getSandboxErr = err
}

// SetGotSandbox sets the sandbox handle returned by MockMsbClient.GetSandbox.
func (m *MockMsbClient) SetGotSandbox(h SandboxHandle) {
	m.gotSandbox = h
}

// MockSandboxHandle is a test double for SandboxHandle.
//
//nolint:revive // underscore suffix avoids Go field/method name conflicts (e.g. Status/Status())
type MockSandboxHandle struct {
	Name_      string
	Status_    msb.SandboxStatus
	UpdatedAt_ time.Time
	Image_     string
	ConnectSb  Sandbox
	StartSb    Sandbox
	DidRmv     bool
	ConnectErr error
	StartErr   error
	StopErr    error
	KillErr    error
	RemoveErr  error
}

func (m *MockSandboxHandle) Name() string              { return m.Name_ }
func (m *MockSandboxHandle) Status() msb.SandboxStatus { return m.Status_ }
func (m *MockSandboxHandle) UpdatedAt() time.Time      { return m.UpdatedAt_ }
func (m *MockSandboxHandle) Image() string             { return m.Image_ }
func (m *MockSandboxHandle) Connect(_ context.Context) (Sandbox, error) {
	if m.ConnectErr != nil {
		return nil, m.ConnectErr
	}
	if m.ConnectSb != nil {
		return m.ConnectSb, nil
	}
	return &MockSandbox{Name_: m.Name_}, nil //nolint:exhaustruct // mock fields are intentionally optional
}
func (m *MockSandboxHandle) Refresh(_ context.Context) (SandboxHandle, error) {
	return m, nil
}
func (m *MockSandboxHandle) Start(_ context.Context) (Sandbox, error) {
	if m.StartErr != nil {
		return nil, m.StartErr
	}
	if m.StartSb != nil {
		return m.StartSb, nil
	}
	return &MockSandbox{Name_: m.Name_}, nil //nolint:exhaustruct // mock fields are intentionally optional
}
func (m *MockSandboxHandle) Stop(_ context.Context, _ ...msb.StopOption) error { return m.StopErr }
func (m *MockSandboxHandle) Kill(_ context.Context, _ ...msb.KillOption) error { return m.KillErr }
func (m *MockSandboxHandle) Remove(_ context.Context) error {
	if m.RemoveErr != nil {
		return m.RemoveErr
	}
	m.DidRmv = true
	return nil
}
func (m *MockSandboxHandle) DidRemove() bool { return m.DidRmv }

// MockSandbox is a test double for Sandbox.
//
//nolint:revive // underscore suffix avoids Go field/method name conflicts
type MockSandbox struct {
	Name_      string
	FSValue_   any
	ShellOut   map[string]ShellResult
	ShellErr   error
	ExecOut    map[string]ShellResult
	ExecErr    error
	AttachCode int
	AttachErr  error
	DetachErr  error
	StopErr    error
	CloseErr   error
}

func (m *MockSandbox) FS() sandboxFS {
	if f, ok := m.FSValue_.(sandboxFS); ok {
		return f
	}
	return nil
}

func (m *MockSandbox) Fs() fsLister {
	if f, ok := m.FSValue_.(fsLister); ok {
		return f
	}
	return &mockFsLister{}
}

// mockFsLister is a no-op fsLister that always returns an empty list.
type mockFsLister struct{}

func (m *mockFsLister) List(_ context.Context, _ string) ([]msb.FsEntry, error) {
	return nil, nil
}

func (m *mockFsLister) Read(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}

func (m *mockFsLister) ReadString(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockFsLister) Exists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

var ErrMockFsListerStat = errors.New("stat not implemented")

func (m *mockFsLister) Stat(_ context.Context, _ string) (*msb.FsStat, error) {
	return nil, ErrMockFsListerStat
}

func (m *mockFsLister) Mkdir(_ context.Context, _ string) error {
	return nil
}

func (m *mockFsLister) Write(_ context.Context, _ string, _ []byte) error {
	return nil
}

func (m *mockFsLister) Remove(_ context.Context, _ string) error {
	return nil
}

func (m *MockSandbox) Shell(_ context.Context, command string, _ ...msb.ExecOption) (ShellResult, error) {
	if m.ShellErr != nil {
		return nil, m.ShellErr
	}
	if out, ok := m.ShellOut[command]; ok {
		return out, nil
	}
	return &mockShellResultImpl{success: true}, nil //nolint:exhaustruct // mock fields are intentionally optional
}

func (m *MockSandbox) Exec(_ context.Context, command string, args []string, _ ...msb.ExecOption) (ShellResult, error) {
	if m.ExecErr != nil {
		return nil, m.ExecErr
	}
	key := command + " " + strings.Join(args, " ")
	if out, ok := m.ExecOut[key]; ok {
		return out, nil
	}
	return &mockShellResultImpl{success: true}, nil //nolint:exhaustruct // mock fields are intentionally optional
}

func (m *MockSandbox) Attach(_ context.Context, _ string, _ ...string) (int, error) {
	return m.AttachCode, m.AttachErr
}

func (m *MockSandbox) Detach(_ context.Context) error                    { return m.DetachErr }
func (m *MockSandbox) Stop(_ context.Context, _ ...msb.StopOption) error { return m.StopErr }
func (m *MockSandbox) Close() error                                      { return m.CloseErr }

// mockShellResultImpl implements ShellResult for tests.
type mockShellResultImpl struct {
	success     bool
	exitCode    int
	stdout      string
	stderr      string
	stdoutBytes []byte
}

func (m *mockShellResultImpl) Success() bool  { return m.success }
func (m *mockShellResultImpl) ExitCode() int  { return m.exitCode }
func (m *mockShellResultImpl) Stdout() string { return m.stdout }
func (m *mockShellResultImpl) Stderr() string { return m.stderr }
func (m *mockShellResultImpl) StdoutBytes() []byte {
	if m.stdoutBytes != nil {
		return m.stdoutBytes
	}
	return []byte(m.stdout)
}

// MockVolumeHandle is a test double for VolumeHandle.
//
//nolint:revive // underscore suffix avoids Go field/method name conflicts
type MockVolumeHandle struct {
	Name_ string
	Path_ string
	Kind_ msb.VolumeKind
}

func (m MockVolumeHandle) Name() string { return m.Name_ }
func (m MockVolumeHandle) Path() string { return m.Path_ }
func (m MockVolumeHandle) Kind() msb.VolumeKind {
	if m.Kind_ == "" {
		return msb.VolumeKindDir
	}
	return m.Kind_
}

// MockImageHandle is a test double for ImageHandle.
//
//nolint:revive // underscore suffix avoids Go field/method name conflicts
type MockImageHandle struct {
	Reference_      string
	ManifestDigest_ string
}

func (m MockImageHandle) Reference() string      { return m.Reference_ }
func (m MockImageHandle) ManifestDigest() string { return m.ManifestDigest_ }

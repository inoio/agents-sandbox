package msb

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// MockMsbClient is a test double for MsbClient with callback-style fields
// and collection fields for convenient test setup. Nil callbacks mean no-op/succeed.
// Existing callers that set Sandboxes/Volumes/Images/List*Err fields work without change.
type MockMsbClient struct {
	mu sync.Mutex

	// Callback-style fields (granular control). Nil = no-op/succeed.
	EnsureInstalledFn func(ctx context.Context) error
	GetSandboxFn      func(ctx context.Context, name string) (SandboxHandle, error)
	CreateSandboxFn   func(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (Sandbox, error)
	ListSandboxesFn   func(ctx context.Context) ([]SandboxHandle, error)
	RemoveSandboxFn   func(ctx context.Context, name string) error
	GetVolumeFn       func(ctx context.Context, name string) (VolumeHandle, error)
	CreateVolumeFn    func(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (VolumeHandle, error)
	ListVolumesFn     func(ctx context.Context) ([]VolumeHandle, error)
	RemoveVolumeFn    func(ctx context.Context, name string) error
	ImageGetFn        func(ctx context.Context, ref string) error
	ImageListFn       func(ctx context.Context) ([]ImageHandle, error)
	ImageRemoveFn     func(ctx context.Context, ref string, force bool) error
	ImageLoadFn       func(ctx context.Context, ref string, r io.Reader) error

	// Pre-populated collections — List* methods return these when the callback is nil.
	// Tests can append to these fields freely.
	Sandboxes []SandboxHandle
	Volumes   []VolumeHandle
	Images    []ImageHandle

	// CreatedSandbox is returned by CreateSandbox instead of the default MockSandbox.
	CreatedSandbox Sandbox

	// CreatedSandboxes holds names passed to CreateSandbox calls.
	CreatedSandboxes []string
	// CreatedSandboxCalls holds full details of CreateSandbox calls.
	CreatedSandboxCalls []MockCreateSandboxCall
	// RemovedSandboxes holds names passed to RemoveSandbox calls.
	RemovedSandboxes []string
	// RemovedVolumes holds names passed to RemoveVolume calls.
	RemovedVolumes []string
	// RemovedImages holds arguments passed to ImageRemove calls.
	RemovedImages []MockRemoveImageCall
	// LoadedImages holds refs passed to ImageLoad calls.
	LoadedImages []string

	// Default return values when no error and no callback/got* is set.
	gotSandbox SandboxHandle
	gotVolume  VolumeHandle

	// Error fields.
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
}

// Compile-time check.
var _ MsbClient = (*MockMsbClient)(nil)

// EnsureInstalled implements MsbClient.
func (m *MockMsbClient) EnsureInstalled(ctx context.Context) error {
	if m.EnsureInstalledFn != nil {
		return m.EnsureInstalledFn(ctx)
	}
	return m.ensureInstalledErr
}

// GetSandbox implements MsbClient.
func (m *MockMsbClient) GetSandbox(ctx context.Context, name string) (SandboxHandle, error) {
	if m.GetSandboxFn != nil {
		return m.GetSandboxFn(ctx, name)
	}
	if m.getSandboxErr != nil {
		return nil, m.getSandboxErr
	}
	if m.gotSandbox != nil {
		return m.gotSandbox, nil
	}
	// Fall back to sandbox handles from Sandboxes collection.
	for _, h := range m.Sandboxes {
		if h.Name() == name {
			return h, nil
		}
	}
	return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
}

// CreateSandbox implements MsbClient.
func (m *MockMsbClient) CreateSandbox(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (Sandbox, error) {
	m.mu.Lock()
	m.CreatedSandboxes = append(m.CreatedSandboxes, name)
	m.CreatedSandboxCalls = append(m.CreatedSandboxCalls, MockCreateSandboxCall{Name: name, Opts: opts})
	m.mu.Unlock()

	if m.CreateSandboxFn != nil {
		return m.CreateSandboxFn(ctx, name, opts...)
	}
	if m.CreateSandboxErr != nil {
		return nil, m.CreateSandboxErr
	}
	if m.CreatedSandbox != nil {
		return m.CreatedSandbox, nil
	}
	//nolint:exhaustruct // tests set only Name_
	return &MockSandbox{Name_: name}, nil
}

// ListSandboxes implements MsbClient.
func (m *MockMsbClient) ListSandboxes(ctx context.Context) ([]SandboxHandle, error) {
	if m.ListSandboxesFn != nil {
		return m.ListSandboxesFn(ctx)
	}
	if m.ListSandboxesErr != nil {
		return nil, m.ListSandboxesErr
	}
	return m.Sandboxes, nil
}

// RemoveSandbox implements MsbClient.
func (m *MockMsbClient) RemoveSandbox(ctx context.Context, name string) error {
	m.mu.Lock()
	m.RemovedSandboxes = append(m.RemovedSandboxes, name)
	m.mu.Unlock()
	if m.RemoveSandboxFn != nil {
		return m.RemoveSandboxFn(ctx, name)
	}
	if m.removeSandboxErr != nil {
		return m.removeSandboxErr
	}
	return nil
}

// GetVolume implements MsbClient.
func (m *MockMsbClient) GetVolume(ctx context.Context, name string) (VolumeHandle, error) {
	if m.GetVolumeFn != nil {
		return m.GetVolumeFn(ctx, name)
	}
	if m.GetVolumeErr != nil {
		return nil, m.GetVolumeErr
	}
	if m.gotVolume != nil {
		return m.gotVolume, nil
	}
	for _, h := range m.Volumes {
		if h.Name() == name {
			return h, nil
		}
	}
	return nil, &msbSdk.Error{Kind: msbSdk.ErrVolumeNotFound, Message: name}
}

// CreateVolume implements MsbClient.
func (m *MockMsbClient) CreateVolume(
	ctx context.Context,
	name string,
	opts ...msbSdk.VolumeOption,
) (VolumeHandle, error) {
	if m.CreateVolumeFn != nil {
		return m.CreateVolumeFn(ctx, name, opts...)
	}
	if m.createVolumeErr != nil {
		return nil, m.createVolumeErr
	}
	//nolint:exhaustruct // tests set only Name_
	return &MockVolumeHandle{Name_: name}, nil
}

// ListVolumes implements MsbClient.
func (m *MockMsbClient) ListVolumes(ctx context.Context) ([]VolumeHandle, error) {
	if m.ListVolumesFn != nil {
		return m.ListVolumesFn(ctx)
	}
	if m.ListVolumesErr != nil {
		return nil, m.ListVolumesErr
	}
	return m.Volumes, nil
}

// RemoveVolume implements MsbClient.
func (m *MockMsbClient) RemoveVolume(ctx context.Context, name string) error {
	m.mu.Lock()
	m.RemovedVolumes = append(m.RemovedVolumes, name)
	m.mu.Unlock()
	if m.RemoveVolumeFn != nil {
		return m.RemoveVolumeFn(ctx, name)
	}
	if m.removeVolumeErr != nil {
		return m.removeVolumeErr
	}
	return nil
}

// ImageGet implements MsbClient.
func (m *MockMsbClient) ImageGet(ctx context.Context, ref string) error {
	if m.ImageGetFn != nil {
		return m.ImageGetFn(ctx, ref)
	}
	return m.imageGetErr
}

// ImageList implements MsbClient.
func (m *MockMsbClient) ImageList(ctx context.Context) ([]ImageHandle, error) {
	if m.ImageListFn != nil {
		return m.ImageListFn(ctx)
	}
	if m.ListImagesErr != nil {
		return nil, m.ListImagesErr
	}
	return m.Images, nil
}

// ImageRemove implements MsbClient.
func (m *MockMsbClient) ImageRemove(ctx context.Context, ref string, force bool) error {
	m.mu.Lock()
	m.RemovedImages = append(m.RemovedImages, MockRemoveImageCall{Ref: ref, Force: force})
	m.mu.Unlock()
	if m.ImageRemoveFn != nil {
		return m.ImageRemoveFn(ctx, ref, force)
	}
	if m.removeImageErr != nil {
		return m.removeImageErr
	}
	return nil
}

// ImageLoad implements MsbClient.
func (m *MockMsbClient) ImageLoad(ctx context.Context, ref string, r io.Reader) error {
	m.mu.Lock()
	m.LoadedImages = append(m.LoadedImages, ref)
	m.mu.Unlock()
	if m.ImageLoadFn != nil {
		return m.ImageLoadFn(ctx, ref, r)
	}
	return m.imageLoadErr
}

// SetGetSandboxErr sets the error returned by MockMsbClient.GetSandbox.
func (m *MockMsbClient) SetGetSandboxErr(err error) *MockMsbClient {
	if err != nil {
		m.GetSandboxFn = func(_ context.Context, _ string) (SandboxHandle, error) {
			return nil, err
		}
	} else {
		m.GetSandboxFn = nil
	}
	return m
}

// SetGotSandbox sets the sandbox handle returned by MockMsbClient.GetSandbox.
func (m *MockMsbClient) SetGotSandbox(h SandboxHandle) *MockMsbClient {
	if h != nil {
		m.GetSandboxFn = func(_ context.Context, _ string) (SandboxHandle, error) {
			return h, nil
		}
	} else {
		m.GetSandboxFn = nil
	}
	return m
}

// SetGetVolumeErr sets the error returned by MockMsbClient.GetVolume.
func (m *MockMsbClient) SetGetVolumeErr(err error) *MockMsbClient {
	if err != nil {
		m.GetVolumeFn = func(_ context.Context, _ string) (VolumeHandle, error) {
			return nil, err
		}
	} else {
		m.GetVolumeFn = nil
	}
	return m
}

// -- Domain mocks --

// MockSandboxHandle is a test double for SandboxHandle.
//
//nolint:revive // underscore names avoid conflicts with interface methods like Status()
type MockSandboxHandle struct {
	Name_           string
	Status_         msbSdk.SandboxStatus
	UpdatedAt_      time.Time
	Image_          string
	ConnectSb       Sandbox
	StartSb         Sandbox
	DidRmv          bool
	ConnectErr      error
	StartErr        error
	StopErr         error
	KillErr         error
	RemoveErr       error
	Cfg             *msbSdk.SandboxConfig
	ModifyErr       error
	Plan            *msbSdk.SandboxModificationPlan
	ModifiedOptions []msbSdk.ModifyOptions
}

func (m *MockSandboxHandle) Name() string                 { return m.Name_ }
func (m *MockSandboxHandle) Status() msbSdk.SandboxStatus { return m.Status_ }
func (m *MockSandboxHandle) UpdatedAt() time.Time         { return m.UpdatedAt_ }
func (m *MockSandboxHandle) Image() string                { return m.Image_ }
func (m *MockSandboxHandle) Connect(_ context.Context) (Sandbox, error) {
	if m.ConnectErr != nil {
		return nil, m.ConnectErr
	}
	if m.ConnectSb != nil {
		return m.ConnectSb, nil
	}
	//nolint:exhaustruct // only Name_ needed
	return &MockSandbox{Name_: m.Name_}, nil
}
func (m *MockSandboxHandle) Refresh(_ context.Context) (SandboxHandle, error) { return m, nil }
func (m *MockSandboxHandle) Start(_ context.Context) (Sandbox, error) {
	if m.StartErr != nil {
		return nil, m.StartErr
	}
	if m.StartSb != nil {
		return m.StartSb, nil
	}
	//nolint:exhaustruct // only Name_ needed
	return &MockSandbox{Name_: m.Name_}, nil
}
func (m *MockSandboxHandle) Stop(_ context.Context, _ ...msbSdk.StopOption) error { return m.StopErr }
func (m *MockSandboxHandle) Kill(_ context.Context, _ ...msbSdk.KillOption) error { return m.KillErr }
func (m *MockSandboxHandle) Remove(_ context.Context) error {
	if m.RemoveErr != nil {
		return m.RemoveErr
	}
	m.DidRmv = true
	return nil
}
func (m *MockSandboxHandle) DidRemove() bool { return m.DidRmv }

func (m *MockSandboxHandle) Config() (*msbSdk.SandboxConfig, error) {
	return m.Cfg, nil
}

func (m *MockSandboxHandle) Modify(
	_ context.Context,
	opts msbSdk.ModifyOptions,
) (*msbSdk.SandboxModificationPlan, error) {
	m.ModifiedOptions = append(m.ModifiedOptions, opts)
	return m.Plan, m.ModifyErr
}

// MockSandbox is a test double for Sandbox.
//
//nolint:revive // underscore names avoid conflicts with interface methods
type MockSandbox struct {
	Name_      string
	FSValue_   any
	ShellOut   map[string]ShellResult
	ShellErr   error
	ShellCalls *[]string
	ExecOut    map[string]ShellResult
	ExecErr    error
	AttachCode int
	AttachErr  error
	DetachErr  error
	StopErr    error
	CloseErr   error
}

func (m *MockSandbox) FS() SandboxFS {
	if f, ok := m.FSValue_.(SandboxFS); ok {
		return f
	}
	return &TestFS{}
}

func (m *MockSandbox) Shell(_ context.Context, command string, _ ...msbSdk.ExecOption) (ShellResult, error) {
	if m.ShellCalls != nil {
		*m.ShellCalls = append(*m.ShellCalls, command)
	}
	if m.ShellErr != nil {
		return nil, m.ShellErr
	}
	if out, ok := m.ShellOut[command]; ok {
		return out, nil
	}
	// Return successful result when no override is configured.
	//nolint:exhaustruct // success-only default
	return &TestResult{success: true}, nil
}
func (m *MockSandbox) Exec(
	_ context.Context,
	command string,
	args []string,
	_ ...msbSdk.ExecOption,
) (ShellResult, error) {
	if m.ExecErr != nil {
		return nil, m.ExecErr
	}
	key := command + " " + strings.Join(args, " ")
	if out, ok := m.ExecOut[key]; ok {
		return out, nil
	}
	// Return successful result when no override is configured.
	//nolint:exhaustruct // success-only default
	return &TestResult{success: true}, nil
}

func (m *MockSandbox) Attach(_ context.Context, _ string, _ ...string) (int, error) {
	return m.AttachCode, m.AttachErr
}

func (m *MockSandbox) Detach(_ context.Context) error                       { return m.DetachErr }
func (m *MockSandbox) Stop(_ context.Context, _ ...msbSdk.StopOption) error { return m.StopErr }
func (m *MockSandbox) Close() error                                         { return m.CloseErr }

// SandboxOpts configures a MockSandbox via NewMockSandbox.
// Zero/unset values produce sensible defaults.
type SandboxOpts struct {
	FSValue    any
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

// NewMockSandbox returns a Sandbox configured by opts. Zero/unset values produce
// sensible defaults so callers only name the fields they care about.
func NewMockSandbox(opts SandboxOpts) Sandbox {
	//nolint:exhaustruct // Name_ is optional for mock construction
	return &MockSandbox{
		FSValue_:   opts.FSValue,
		ShellOut:   opts.ShellOut,
		ShellErr:   opts.ShellErr,
		ExecOut:    opts.ExecOut,
		ExecErr:    opts.ExecErr,
		AttachCode: opts.AttachCode,
		AttachErr:  opts.AttachErr,
		DetachErr:  opts.DetachErr,
		StopErr:    opts.StopErr,
		CloseErr:   opts.CloseErr,
	}
}

// TestFS is a test double for SandboxFS that supports configurable file contents,
// listing, and error injection.
type TestFS struct {
	Contents map[string][]byte
	LS       []msbSdk.FsEntry
	ReadErr  error
	ListErr  error
	Writes   map[string][]byte
}

// NewTestFS creates a SandboxFS backed by the given files. Files is a map from
// path to file content; ls is the return value for List. Nil map values
// produce sensible defaults.
func NewTestFS(files map[string][]byte, ls []msbSdk.FsEntry) *TestFS {
	//nolint:exhaustruct // ReadErr and ListErr default to zero value (nil)
	return &TestFS{Contents: files, LS: ls}
}

// SetReadErr configures Read/ReadStream/String to return the given error.
func (t *TestFS) SetReadErr(err error) *TestFS { t.ReadErr = err; return t }

// SetListErr configures List to return the given error.
func (t *TestFS) SetListErr(err error) *TestFS { t.ListErr = err; return t }

func (t *TestFS) Exists(_ context.Context, path string) (bool, error) {
	_, ok := t.Contents[path]
	return ok, nil
}

func (t *TestFS) Stat(_ context.Context, _ string) (*msbSdk.FsStat, error) {
	return &msbSdk.FsStat{}, nil
}

func (t *TestFS) List(_ context.Context, _ string) ([]msbSdk.FsEntry, error) {
	if t.ListErr != nil {
		return nil, t.ListErr
	}
	return t.LS, nil
}

func (t *TestFS) ReadString(_ context.Context, path string) (string, error) {
	if t.ReadErr != nil {
		return "", t.ReadErr
	}
	if d, ok := t.Contents[path]; ok {
		return string(d), nil
	}
	return "", fmt.Errorf("file not found: %s", path)
}

func (t *TestFS) ReadStream(_ context.Context, _ string) (*msbSdk.FsReadStream, error) {
	if t.ReadErr != nil {
		return nil, t.ReadErr
	}
	return &msbSdk.FsReadStream{}, nil
}

func (t *TestFS) Mkdir(_ context.Context, _ string) error { return nil }
func (t *TestFS) Write(_ context.Context, path string, data []byte) error {
	if t.Writes == nil {
		t.Writes = make(map[string][]byte)
	}
	t.Writes[path] = data
	return nil
}
func (t *TestFS) Read(_ context.Context, path string) ([]byte, error) {
	if t.ReadErr != nil {
		return nil, t.ReadErr
	}
	if d, ok := t.Contents[path]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("file not found: %s", path)
}
func (t *TestFS) Remove(_ context.Context, _ string) error { return nil }

// TestResult implements ShellResult for tests.
type TestResult struct {
	success     bool
	exitCode    int
	stdout      string
	stderr      string
	stdoutBytes []byte
}

// NewTestResult creates a ShellResult for tests.
func NewTestResult(success bool, exitCode int, stdout, stderr string, stdoutBytes []byte) ShellResult {
	return &TestResult{success: success, exitCode: exitCode, stdout: stdout, stderr: stderr, stdoutBytes: stdoutBytes}
}

func (t *TestResult) Success() bool  { return t.success }
func (t *TestResult) ExitCode() int  { return t.exitCode }
func (t *TestResult) Stdout() string { return t.stdout }
func (t *TestResult) Stderr() string { return t.stderr }
func (t *TestResult) StdoutBytes() []byte {
	if t.stdoutBytes != nil {
		return t.stdoutBytes
	}
	return []byte(t.stdout)
}

//nolint:revive // underscore names avoid conflicts with interface methods
type MockVolumeHandle struct {
	Name_ string
	Path_ string
	Kind_ msbSdk.VolumeKind
}

func (m MockVolumeHandle) Name() string { return m.Name_ }
func (m MockVolumeHandle) Path() string { return m.Path_ }
func (m MockVolumeHandle) Kind() msbSdk.VolumeKind {
	if m.Kind_ == "" {
		return msbSdk.VolumeKindDir
	}
	return m.Kind_
}

//nolint:revive // underscore names avoid conflicts with interface methods
type MockImageHandle struct {
	Reference_      string
	ManifestDigest_ string
}

func (m MockImageHandle) Reference() string      { return m.Reference_ }
func (m MockImageHandle) ManifestDigest() string { return m.ManifestDigest_ }

// WithMsbMock replaces the global Get factory with the provided mock.
// It restores the original factory when the test ends.
func WithMsbMock(t *testing.T, mock MsbClient) {
	t.Helper()
	orig := Get
	Get = func() MsbClient { return mock }
	t.Cleanup(func() { Get = orig })
}

// ResetGetFn replaces the global Get factory, returning the previous factory for restoration.
func ResetGetFn(f func() MsbClient) func() MsbClient {
	old := Get
	Get = f
	return old
}

// WithNoopMsbMock replaces Get with a MockMsbClient where every method succeeds.
func WithNoopMsbMock(t *testing.T) {
	//nolint:exhaustruct // zero value is intentionally minimal
	WithMsbMock(t, &MockMsbClient{})
}

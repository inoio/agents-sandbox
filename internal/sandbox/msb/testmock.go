package msb

import (
	"context"
	"io"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// MockSandboxHandle is a mock implementation of SandboxHandle for tests.
type MockSandboxHandle struct {
	NameFunc      func() string
	StatusFunc    func() msbSdk.SandboxStatus
	UpdatedAtFunc func() time.Time
	ImageFunc     func() string
	ConnectFunc   func(ctx context.Context) (Sandbox, error)
	RefreshFunc   func(ctx context.Context) (SandboxHandle, error)
	StartFunc     func(ctx context.Context) (Sandbox, error)
	StopFunc      func(ctx context.Context, opts ...msbSdk.StopOption) error
	KillFunc      func(ctx context.Context, opts ...msbSdk.KillOption) error
	RemoveFunc    func(ctx context.Context) error
	Context       context.Context
}

func (m *MockSandboxHandle) Name() string {
	if m.NameFunc != nil {
		return m.NameFunc()
	}
	return ""
}

func (m *MockSandboxHandle) Status() msbSdk.SandboxStatus {
	if m.StatusFunc != nil {
		return m.StatusFunc()
	}
	return msbSdk.SandboxStatusStopped
}

func (m *MockSandboxHandle) UpdatedAt() time.Time {
	if m.UpdatedAtFunc != nil {
		return m.UpdatedAtFunc()
	}
	return time.Time{}
}

func (m *MockSandboxHandle) Image() string {
	if m.ImageFunc != nil {
		return m.ImageFunc()
	}
	return ""
}

func (m *MockSandboxHandle) Connect(ctx context.Context) (Sandbox, error) {
	if m.ConnectFunc != nil {
		return m.ConnectFunc(ctx)
	}
	return nil, nil
}

func (m *MockSandboxHandle) Refresh(ctx context.Context) (SandboxHandle, error) {
	if m.RefreshFunc != nil {
		return m.RefreshFunc(ctx)
	}
	return m, nil
}

func (m *MockSandboxHandle) Start(ctx context.Context) (Sandbox, error) {
	if m.StartFunc != nil {
		return m.StartFunc(ctx)
	}
	return nil, nil
}

func (m *MockSandboxHandle) Stop(ctx context.Context, opts ...msbSdk.StopOption) error {
	if m.StopFunc != nil {
		return m.StopFunc(ctx, opts...)
	}
	return nil
}

func (m *MockSandboxHandle) Kill(ctx context.Context, opts ...msbSdk.KillOption) error {
	if m.KillFunc != nil {
		return m.KillFunc(ctx, opts...)
	}
	return nil
}

func (m *MockSandboxHandle) Remove(ctx context.Context) error {
	if m.RemoveFunc != nil {
		return m.RemoveFunc(ctx)
	}
	return nil
}

// MockSandbox is a mock implementation of Sandbox for tests.
type MockSandbox struct {
	FSFunc     func() SandboxFS
	ShellFunc  func(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error)
	ExecFunc   func(ctx context.Context, command string, args []string, opts ...msbSdk.ExecOption) (ShellResult, error)
	AttachFunc func(ctx context.Context, command string, args ...string) (int, error)
	DetachFunc func(ctx context.Context) error
	StopFunc   func(ctx context.Context, opts ...msbSdk.StopOption) error
	CloseFunc  func() error
}

func (m *MockSandbox) FS() SandboxFS {
	if m.FSFunc != nil {
		return m.FSFunc()
	}
	return nil
}

func (m *MockSandbox) Shell(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error) {
	if m.ShellFunc != nil {
		return m.ShellFunc(ctx, command, opts...)
	}
	return nil, nil
}

func (m *MockSandbox) Exec(ctx context.Context, command string, args []string, opts ...msbSdk.ExecOption) (ShellResult, error) {
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, command, args, opts...)
	}
	return nil, nil
}

func (m *MockSandbox) Attach(ctx context.Context, command string, args ...string) (int, error) {
	if m.AttachFunc != nil {
		return m.AttachFunc(ctx, command, args...)
	}
	return 0, nil
}

func (m *MockSandbox) Detach(ctx context.Context) error {
	if m.DetachFunc != nil {
		return m.DetachFunc(ctx)
	}
	return nil
}

func (m *MockSandbox) Stop(ctx context.Context, opts ...msbSdk.StopOption) error {
	if m.StopFunc != nil {
		return m.StopFunc(ctx, opts...)
	}
	return nil
}

func (m *MockSandbox) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

// MockMsbClient is a mock implementation of MsbClient for tests.
type MockMsbClient struct {
	EnsureInstalledFunc func(ctx context.Context) error
	GetSandboxFunc      func(ctx context.Context, name string) (SandboxHandle, error)
	CreateSandboxFunc   func(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (Sandbox, error)
	ListSandboxesFunc   func(ctx context.Context) ([]SandboxHandle, error)
	RemoveSandboxFunc   func(ctx context.Context, name string) error
	GetVolumeFunc       func(ctx context.Context, name string) (VolumeHandle, error)
	CreateVolumeFunc    func(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (VolumeHandle, error)
	ListVolumesFunc     func(ctx context.Context) ([]VolumeHandle, error)
	RemoveVolumeFunc    func(ctx context.Context, name string) error
	ImageGetFunc        func(ctx context.Context, ref string) error
	ImageListFunc       func(ctx context.Context) ([]ImageHandle, error)
	ImageRemoveFunc     func(ctx context.Context, ref string, force bool) error
	ImageLoadFunc       func(ctx context.Context, ref string, r io.Reader) error

	// Calls tracks the calls made to this mock.
	CreateCall      MockCreateSandboxCall
	RemoveImageCall MockRemoveImageCall
}

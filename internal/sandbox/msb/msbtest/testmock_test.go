package msbtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestWithMsbMock pins that WithMsbMock installs the mock as msb.Get for one
// test and restores the previous factory when the test ends.
func TestWithMsbMock(t *testing.T) {
	mock := &MockMsbClient{}
	t.Run("install", func(t *testing.T) {
		WithMsbMock(t, mock)
		if msb.Get() != mock {
			t.Fatal("Get should return the mock after WithMsbMock")
		}
	})
	if msb.Get() == mock {
		t.Error("WithMsbMock must restore the previous factory on cleanup")
	}
}

// TestMockClientIsNotReal pins that an installed mock is not reported as the
// real SDK client, so a test can assert production code took the mock path.
func TestMockClientIsNotReal(t *testing.T) {
	WithMsbMock(t, &MockMsbClient{})
	if msb.IsRealClient() {
		t.Fatal("a mock client must not report as real")
	}
}

// TestFailFastClientPanics pins that every method of the fail-fast client
// panics, so a test that reaches msb without installing a mock fails loudly
// instead of touching the real runtime.
func TestFailFastClientPanics(t *testing.T) {
	ctx := context.Background()
	f := &failFastMsbClient{}
	cases := []struct {
		name string
		fn   func()
	}{
		{"EnsureInstalled", func() { _ = f.EnsureInstalled(ctx) }},
		{"GetSandbox", func() { _, _ = f.GetSandbox(ctx, "x") }},
		{"CreateSandbox", func() { _, _ = f.CreateSandbox(ctx, "x", msb.SandboxSpec{}) }},
		{"ListSandboxes", func() { _, _ = f.ListSandboxes(ctx, nil) }},
		{"GetVolume", func() { _, _ = f.GetVolume(ctx, "x") }},
		{"CreateVolume", func() { _, _ = f.CreateVolume(ctx, "x", msb.VolumeSpec{}) }},
		{"ListVolumes", func() { _, _ = f.ListVolumes(ctx) }},
		{"RemoveVolume", func() { _ = f.RemoveVolume(ctx, "x") }},
		{"ImageGet", func() { _ = f.ImageGet(ctx, "x") }},
		{"ImageList", func() { _, _ = f.ImageList(ctx) }},
		{"ImagePrune", func() { _, _ = f.ImagePrune(ctx) }},
		{"PruneStorage", func() { _, _ = f.PruneStorage(ctx, msb.PruneRequest{}) }},
		{"ImageRemove", func() { _ = f.ImageRemove(ctx, "x", false) }},
		{"ImageLoad", func() { _ = f.ImageLoad(ctx, "x", strings.NewReader("")) }},
		{"ImageInspect", func() { _, _ = f.ImageInspect(ctx, "x") }},
		{"ImageInspectDetail", func() { _, _ = f.ImageInspectDetail(ctx, "x") }},
		{"StorageUsage", func() { _, _ = f.StorageUsage(ctx) }},
		{"ShellStream", func() { _, _ = f.ShellStream(ctx, "cmd", msb.ExecRequest{}) }},
		{"ExecStream", func() { _, _ = f.ExecStream(ctx, "cmd", nil, msb.ExecRequest{}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic from fail-fast client")
				}
			}()
			tc.fn()
		})
	}
}

// TestInstallFailFastGet pins that installing the fail-fast client replaces
// whatever msb.Get returned, so msb.Get panics until a test installs a mock,
// and works once one is.
func TestInstallFailFastGet(t *testing.T) {
	t.Run("replaces a factory", func(t *testing.T) {
		WithMsbMock(t, &MockMsbClient{})
		InstallFailFastGet()
		if _, ok := msb.Get().(*failFastMsbClient); !ok {
			t.Fatalf("msb.Get() = %T after InstallFailFastGet, want *failFastMsbClient", msb.Get())
		}
		defer func() {
			if recover() == nil {
				t.Fatal("msb.Get did not panic after InstallFailFastGet")
			}
		}()
		_ = msb.Get().EnsureInstalled(context.Background())
	})
	t.Run("with a mock", func(t *testing.T) {
		WithMsbMock(t, &MockMsbClient{})
		if err := msb.Get().EnsureInstalled(context.Background()); err != nil {
			t.Fatalf("EnsureInstalled through the mock: %v", err)
		}
	})
}

func TestWithCountingMsbMockCountsResolutionsAndRestores(t *testing.T) {
	mock := &MockMsbClient{}

	t.Run("install", func(t *testing.T) {
		resolutions := WithCountingMsbMock(t, mock)
		if *resolutions != 0 {
			t.Fatalf("resolutions before any Get = %d, want 0", *resolutions)
		}
		for range 2 {
			if msb.Get() != mock {
				t.Fatal("Get should return the mock after WithCountingMsbMock")
			}
		}
		if *resolutions != 2 {
			t.Errorf("resolutions = %d, want 2", *resolutions)
		}
	})

	if msb.Get() == mock {
		t.Error("WithCountingMsbMock must restore the previous factory on cleanup")
	}
}

// TestMockSandboxHandleConfigMounts pins that the handle double returns its
// seeded mounts and surfaces a configured error.
func TestMockSandboxHandleConfigMounts(t *testing.T) {
	want := map[string]msb.MountConfig{
		"/tmp": msb.Mount.Tmpfs(msb.TmpfsOptions{SizeMiB: 2048}),
	}
	m := &MockSandboxHandle{Mounts: want}
	got, err := m.ConfigMounts()
	if err != nil {
		t.Fatalf("ConfigMounts() error = %v", err)
	}
	if got["/tmp"].SizeMiB != 2048 {
		t.Errorf("ConfigMounts() = %+v, want the seeded mounts", got)
	}

	failing := &MockSandboxHandle{MountsErr: errors.New("mounts boom")}
	if _, err := failing.ConfigMounts(); err == nil {
		t.Error("ConfigMounts() error = nil, want the configured MountsErr")
	}
}

// TestMockSandboxHandleConfigPorts pins that the handle double returns its
// seeded bindings and surfaces a configured error.
func TestMockSandboxHandleConfigPorts(t *testing.T) {
	want := []msb.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: msb.PortProtocolTCP},
	}
	m := &MockSandboxHandle{PortBindings: want}
	got, err := m.ConfigPorts()
	if err != nil {
		t.Fatalf("ConfigPorts() error = %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("ConfigPorts() = %+v, want the seeded bindings", got)
	}

	failing := &MockSandboxHandle{PortsErr: errors.New("ports boom")}
	if _, err := failing.ConfigPorts(); err == nil {
		t.Error("ConfigPorts() error = nil, want the configured PortsErr")
	}
}

// TestMockSandboxMetricsAndLogs pins the sandbox double's Metrics and Logs
// delegation, including the recorded LogQuery and the error branches.
func TestMockSandboxMetricsAndLogs(t *testing.T) {
	ctx := context.Background()

	sb := &MockSandbox{MetricsResult: &msb.Metrics{MemoryBytes: 1}}
	got, err := sb.Metrics(ctx)
	if err != nil || got.MemoryBytes != 1 {
		t.Fatalf("Metrics() = (%v, %v); want MemoryBytes 1", got, err)
	}

	boom := errors.New("boom")
	if _, err := (&MockSandbox{MetricsErr: boom}).Metrics(ctx); !errors.Is(err, boom) {
		t.Fatalf("Metrics() error = %v; want boom", err)
	}

	entries := []msb.LogEntry{{Source: msb.LogSourceSystem}}
	logSB := &MockSandbox{LogsResult: entries}
	gotEntries, err := logSB.Logs(ctx, msb.LogQuery{Tail: 3})
	if err != nil || len(gotEntries) != 1 {
		t.Fatalf("Logs() = (%v, %v); want one entry, nil", gotEntries, err)
	}
	if len(logSB.LogsOptions) != 1 || logSB.LogsOptions[0].Tail != 3 {
		t.Fatalf("LogsOptions = %+v; want one call with Tail 3", logSB.LogsOptions)
	}
	if _, err := (&MockSandbox{LogsErr: boom}).Logs(ctx, msb.LogQuery{}); !errors.Is(err, boom) {
		t.Fatalf("Logs() error = %v; want boom", err)
	}
}

// TestMockSandboxHandleMetricsAndLogs pins the handle double's Metrics and Logs
// delegation, including the recorded LogQuery and the error branches.
func TestMockSandboxHandleMetricsAndLogs(t *testing.T) {
	ctx := context.Background()

	h := &MockSandboxHandle{MetricsResult: &msb.Metrics{MemoryLimitBytes: 2}}
	got, err := h.Metrics(ctx)
	if err != nil || got.MemoryLimitBytes != 2 {
		t.Fatalf("Metrics() = (%v, %v); want MemoryLimitBytes 2", got, err)
	}

	boom := errors.New("boom")
	if _, err := (&MockSandboxHandle{MetricsErr: boom}).Metrics(ctx); !errors.Is(err, boom) {
		t.Fatalf("Metrics() error = %v; want boom", err)
	}

	logHandle := &MockSandboxHandle{LogsResult: []msb.LogEntry{{Source: msb.LogSourceSystem}}}
	gotEntries, err := logHandle.Logs(ctx, msb.LogQuery{Tail: 9})
	if err != nil || len(gotEntries) != 1 {
		t.Fatalf("Logs() = (%v, %v); want one entry, nil", gotEntries, err)
	}
	if len(logHandle.LogsOptions) != 1 || logHandle.LogsOptions[0].Tail != 9 {
		t.Fatalf("LogsOptions = %+v; want one call with Tail 9", logHandle.LogsOptions)
	}
	if _, err := (&MockSandboxHandle{LogsErr: boom}).Logs(ctx, msb.LogQuery{}); !errors.Is(err, boom) {
		t.Fatalf("Logs() error = %v; want boom", err)
	}
}

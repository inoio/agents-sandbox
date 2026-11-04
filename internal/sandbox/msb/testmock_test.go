package msb

import (
	"context"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// TestWithMsbMock pins that WithMsbMock installs the mock as msb.Get for one
// test and restores the previous factory when the test ends.
func TestWithMsbMock(t *testing.T) {
	mock := &MockMsbClient{}
	t.Run("install", func(t *testing.T) {
		WithMsbMock(t, mock)
		if Get() != mock {
			t.Fatal("Get should return the mock after WithMsbMock")
		}
	})
	if Get() == mock {
		t.Error("WithMsbMock must restore the previous factory on cleanup")
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
		{"CreateSandbox", func() { _, _ = f.CreateSandbox(ctx, "x") }},
		{"ListSandboxes", func() { _, _ = f.ListSandboxes(ctx, nil) }},
		{"GetVolume", func() { _, _ = f.GetVolume(ctx, "x") }},
		{"CreateVolume", func() { _, _ = f.CreateVolume(ctx, "x") }},
		{"ListVolumes", func() { _, _ = f.ListVolumes(ctx) }},
		{"RemoveVolume", func() { _ = f.RemoveVolume(ctx, "x") }},
		{"ImageGet", func() { _ = f.ImageGet(ctx, "x") }},
		{"ImageList", func() { _, _ = f.ImageList(ctx) }},
		{"ImagePrune", func() { _, _ = f.ImagePrune(ctx) }},
		{"PruneStorage", func() { _, _ = f.PruneStorage(ctx, msbSdk.StoragePruneOptions{}) }},
		{"ImageRemove", func() { _ = f.ImageRemove(ctx, "x", false) }},
		{"ImageLoad", func() { _ = f.ImageLoad(ctx, "x", strings.NewReader("")) }},
		{"ImageInspect", func() { _, _ = f.ImageInspect(ctx, "x") }},
		{"ImageInspectDetail", func() { _, _ = f.ImageInspectDetail(ctx, "x") }},
		{"StorageUsage", func() { _, _ = f.StorageUsage(ctx) }},
		{"ShellStream", func() { _, _ = f.ShellStream(ctx, "cmd") }},
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
		defer func() {
			if recover() == nil {
				t.Fatal("msb.Get did not panic after InstallFailFastGet")
			}
		}()
		_ = Get().EnsureInstalled(context.Background())
	})
	t.Run("with a mock", func(t *testing.T) {
		WithMsbMock(t, &MockMsbClient{})
		if err := Get().EnsureInstalled(context.Background()); err != nil {
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
			if Get() != mock {
				t.Fatal("Get should return the mock after WithCountingMsbMock")
			}
		}
		if *resolutions != 2 {
			t.Errorf("resolutions = %d, want 2", *resolutions)
		}
	})

	if Get() == mock {
		t.Error("WithCountingMsbMock must restore the previous factory on cleanup")
	}
}

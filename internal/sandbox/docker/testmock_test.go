package docker

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/moby/moby/client"
)

// TestMockDockerClientDefaults verifies the zero MockDockerClient succeeds on
// every method with the default (empty) results.
func TestMockDockerClientDefaults(t *testing.T) {
	m := &MockDockerClient{}
	ctx := context.Background()

	if _, err := m.ImageBuild(ctx, io.NopCloser(nil), client.ImageBuildOptions{}); err != nil {
		t.Errorf("ImageBuild default error = %v, want nil", err)
	}
	if _, err := m.ImageInspect(ctx, "ref"); err != nil {
		t.Errorf("ImageInspect default error = %v, want nil", err)
	}
	save, err := m.ImageSave(ctx, []string{"ref"})
	if err != nil || save == nil {
		t.Errorf("ImageSave default = %v, %v, want non-nil, nil", save, err)
	}
	if _, err := m.ImageRemove(ctx, "ref", client.ImageRemoveOptions{}); err != nil {
		t.Errorf("ImageRemove default error = %v, want nil", err)
	}
	if _, err := m.ImagePull(ctx, "ref", client.ImagePullOptions{}); err != nil {
		t.Errorf("ImagePull default error = %v, want nil", err)
	}
	if _, err := m.ImageTag(ctx, client.ImageTagOptions{}); err != nil {
		t.Errorf("ImageTag default error = %v, want nil", err)
	}
	if _, err := m.ImagePrune(ctx, client.ImagePruneOptions{}); err != nil {
		t.Errorf("ImagePrune default error = %v, want nil", err)
	}
	ping, err := m.Ping(ctx, client.PingOptions{})
	if err != nil || ping.OSType != "linux" {
		t.Errorf("Ping default = %+v, %v, want linux, nil", ping, err)
	}
}

// TestMockDockerClientFns verifies each method delegates to its injected Fn
// when set.
func TestMockDockerClientFns(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("boom")
	m := &MockDockerClient{
		ImageBuildFn: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, wantErr
		},
		ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, wantErr
		},
		ImageSaveFn: func(context.Context, []string, ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, wantErr
		},
		ImageRemoveFn: func(context.Context, string, client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
			return client.ImageRemoveResult{}, wantErr
		},
		ImagePullFn: func(context.Context, string, client.ImagePullOptions) (io.ReadCloser, error) {
			return nil, wantErr
		},
		ImageTagFn: func(context.Context, client.ImageTagOptions) (client.ImageTagResult, error) {
			return client.ImageTagResult{}, wantErr
		},
		ImagePruneFn: func(context.Context, client.ImagePruneOptions) (client.ImagePruneResult, error) {
			return client.ImagePruneResult{}, wantErr
		},
		PingFn: func(context.Context, client.PingOptions) (client.PingResult, error) {
			return client.PingResult{}, wantErr
		},
	}

	if _, err := m.ImageBuild(ctx, nil, client.ImageBuildOptions{}); !errors.Is(err, wantErr) {
		t.Errorf("ImageBuild err = %v, want %v", err, wantErr)
	}
	if _, err := m.ImageInspect(ctx, "ref"); !errors.Is(err, wantErr) {
		t.Errorf("ImageInspect err = %v, want %v", err, wantErr)
	}
	if _, err := m.ImageSave(ctx, nil); !errors.Is(err, wantErr) {
		t.Errorf("ImageSave err = %v, want %v", err, wantErr)
	}
	if _, err := m.ImageRemove(ctx, "ref", client.ImageRemoveOptions{}); !errors.Is(err, wantErr) {
		t.Errorf("ImageRemove err = %v, want %v", err, wantErr)
	}
	if _, err := m.ImagePull(ctx, "ref", client.ImagePullOptions{}); !errors.Is(err, wantErr) {
		t.Errorf("ImagePull err = %v, want %v", err, wantErr)
	}
	if _, err := m.ImageTag(ctx, client.ImageTagOptions{}); !errors.Is(err, wantErr) {
		t.Errorf("ImageTag err = %v, want %v", err, wantErr)
	}
	if _, err := m.ImagePrune(ctx, client.ImagePruneOptions{}); !errors.Is(err, wantErr) {
		t.Errorf("ImagePrune err = %v, want %v", err, wantErr)
	}
	if _, err := m.Ping(ctx, client.PingOptions{}); !errors.Is(err, wantErr) {
		t.Errorf("Ping err = %v, want %v", err, wantErr)
	}
}

// TestNewErrorDockerClient verifies the error mock returns a non-nil error from
// every operation.
func TestNewErrorDockerClient(t *testing.T) {
	ctx := context.Background()
	m := newDefaultErrorDockerClient()
	if _, err := m.ImageBuild(ctx, nil, client.ImageBuildOptions{}); err == nil {
		t.Error("ImageBuild should error")
	}
	if _, err := m.ImageInspect(ctx, "ref"); err == nil {
		t.Error("ImageInspect should error")
	}
	if _, err := m.ImageSave(ctx, nil); err == nil {
		t.Error("ImageSave should error")
	}
	if _, err := m.ImageRemove(ctx, "ref", client.ImageRemoveOptions{}); err == nil {
		t.Error("ImageRemove should error")
	}
	// ImagePull always uses the default error in the error mock.
	if _, err := m.ImagePull(ctx, "ref", client.ImagePullOptions{}); err == nil {
		t.Error("ImagePull should error")
	}
	// ImageTagFn is intentionally nil in the error mock, so ImageTag succeeds
	// via the default branch.
	if _, err := m.ImageTag(ctx, client.ImageTagOptions{}); err != nil {
		t.Errorf("ImageTag err = %v, want nil (default branch)", err)
	}
	if _, err := m.ImagePrune(ctx, client.ImagePruneOptions{}); err == nil {
		t.Error("ImagePrune should error")
	}
	if _, err := m.Ping(ctx, client.PingOptions{}); err == nil {
		t.Error("Ping should error")
	}
}

// TestNewErrorDockerClientCustomErrors verifies per-method error overrides.
func TestNewErrorDockerClientCustomErrors(t *testing.T) {
	ctx := context.Background()
	custom := errors.New("custom")
	m := newErrorDockerClient(mockErrors{buildErr: custom})

	if _, err := m.ImageBuild(ctx, nil, client.ImageBuildOptions{}); !errors.Is(err, custom) {
		t.Errorf("ImageBuild err = %v, want %v", err, custom)
	}
	if _, err := m.ImageInspect(ctx, "ref"); errors.Is(err, custom) {
		t.Error("ImageInspect should not use the build override")
	}
}

// TestNewErrorDockerClientAllOverrides verifies that each per-method error
// override branch is honored, so the default daemon error is never returned
// when a custom error is supplied for that method.
func TestNewErrorDockerClientAllOverrides(t *testing.T) {
	ctx := context.Background()
	overrides := map[string]error{
		"build":   errors.New("build custom"),
		"inspect": errors.New("inspect custom"),
		"save":    errors.New("save custom"),
		"remove":  errors.New("remove custom"),
	}
	m := newErrorDockerClient(mockErrors{
		buildErr:   overrides["build"],
		inspectErr: overrides["inspect"],
		saveErr:    overrides["save"],
		removeErr:  overrides["remove"],
	})

	checks := map[string]func() error{
		"build":   func() error { _, err := m.ImageBuild(ctx, nil, client.ImageBuildOptions{}); return err },
		"inspect": func() error { _, err := m.ImageInspect(ctx, "ref"); return err },
		"save":    func() error { _, err := m.ImageSave(ctx, nil); return err },
		"remove":  func() error { _, err := m.ImageRemove(ctx, "ref", client.ImageRemoveOptions{}); return err },
	}
	for name, want := range overrides {
		if err := checks[name](); !errors.Is(err, want) {
			t.Errorf("%s err = %v, want %v", name, err, want)
		}
	}
}

// TestWithDockerMockAndNoop verifies WithNoopDockerMock installs a mock and
// restores Get after the test.
func TestWithDockerMockAndNoop(t *testing.T) {
	orig := Get
	WithNoopDockerMock(t)
	if Get() == orig() {
		t.Error("Get should return the mock after WithNoopDockerMock")
	}
}

// TestWithDefaultErrorDockerMockInstalls verifies the helper installs an
// erroring mock and restores Get after the test.
func TestWithDefaultErrorDockerMockInstalls(t *testing.T) {
	orig := Get
	WithDefaultErrorDockerMock(t)
	if Get() == orig() {
		t.Error("Get should return the error mock after WithDefaultErrorDockerMock")
	}
}

// TestFailFastDockerClientPanics verifies every failFastDockerClient method
// panics, signalling a test reached the real docker client without opting in.
func TestFailFastDockerClientPanics(t *testing.T) {
	f := &failFastDockerClient{}
	ctx := context.Background()

	tests := []struct {
		name string
		call func()
	}{
		{"ImageBuild", func() { _, _ = f.ImageBuild(ctx, nil, client.ImageBuildOptions{}) }},
		{"ImageInspect", func() { _, _ = f.ImageInspect(ctx, "ref") }},
		{"ImageSave", func() { _, _ = f.ImageSave(ctx, nil) }},
		{"ImagePull", func() { _, _ = f.ImagePull(ctx, "ref", client.ImagePullOptions{}) }},
		{"ImageRemove", func() { _, _ = f.ImageRemove(ctx, "ref", client.ImageRemoveOptions{}) }},
		{"ImageTag", func() { _, _ = f.ImageTag(ctx, client.ImageTagOptions{}) }},
		{"ImagePrune", func() { _, _ = f.ImagePrune(ctx, client.ImagePruneOptions{}) }},
		{"Ping", func() { _, _ = f.Ping(ctx, client.PingOptions{}) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s should panic via mustMock", tc.name)
				}
			}()
			tc.call()
		})
	}
}

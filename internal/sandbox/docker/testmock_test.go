package docker

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

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

// TestWithDockerMockRestoresGet verifies WithDockerMock's cleanup restores the
// previously installed factory, so a later test does not inherit a leaked mock.
func TestWithDockerMockRestoresGet(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		WithDockerMock(t, &MockDockerClient{})
	})

	if _, ok := Get().(*failFastDockerClient); !ok {
		t.Errorf("Get() = %T after the installing test, want the restored *failFastDockerClient", Get())
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
		{"ImageList", func() { _, _ = f.ImageList(ctx, client.ImageListOptions{}) }},
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

// TestMockDockerClientImageList verifies the mock returns ImageListFn's items
// when set, passes the options through, and returns an empty result when the
// fn is nil.
func TestMockDockerClientImageList(t *testing.T) {
	ctx := context.Background()
	opts := client.ImageListOptions{All: true, SharedSize: true}

	items := []image.Summary{
		{
			ID:      "sha256:abc123",
			Created: 1_700_000_000,
			Labels:  map[string]string{"org.agents-sandbox.managed": "true"},
			RepoTags: []string{
				"agents-sandbox/example:latest",
			},
		},
	}

	t.Run("calls ImageListFn with the options", func(t *testing.T) {
		m := &MockDockerClient{
			ImageListFn: func(gotCtx context.Context, gotOpts client.ImageListOptions) (client.ImageListResult, error) {
				if gotCtx != ctx {
					t.Errorf("ImageList ctx = %v, want %v", gotCtx, ctx)
				}
				if !reflect.DeepEqual(gotOpts, opts) {
					t.Errorf("ImageList options = %+v, want %+v", gotOpts, opts)
				}
				return client.ImageListResult{Items: items}, nil
			},
		}

		got, err := m.ImageList(ctx, opts)
		if err != nil {
			t.Fatalf("ImageList() error = %v", err)
		}
		if !reflect.DeepEqual(got.Items, items) {
			t.Errorf("ImageList() items = %+v, want %+v", got.Items, items)
		}
	})

	t.Run("nil fn returns empty result", func(t *testing.T) {
		got, err := (&MockDockerClient{}).ImageList(ctx, opts)
		if err != nil {
			t.Fatalf("ImageList() error = %v", err)
		}
		if got.Items != nil {
			t.Errorf("ImageList() items = %+v, want nil", got.Items)
		}
	})
}

// TestInstallFailFastGetPanicsWithAPointerToWithDockerMock pins that once the
// fail-fast client is installed, reaching docker.Get without opting into a mock
// panics, and that the panic tells the test author which helper to use.
func TestInstallFailFastGetPanicsWithAPointerToWithDockerMock(t *testing.T) {
	defer func() {
		msg := recover()
		if msg == nil {
			t.Fatal("fail-fast docker.Get should panic when no mock is installed, but did not")
		}
		if s, ok := msg.(string); !ok || !strings.Contains(s, "WithDockerMock") {
			t.Fatalf("panic message should mention WithDockerMock, got %v", msg)
		}
	}()
	InstallFailFastGet()
	Get().Ping(context.Background(), client.PingOptions{})
}

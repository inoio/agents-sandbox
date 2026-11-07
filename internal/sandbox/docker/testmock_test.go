package docker

import (
	"context"
	"strings"
	"testing"

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

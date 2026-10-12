package builder

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

type fakeDockerCLI struct {
	calls   [][]string
	outputs map[string]string
	errs    map[string]error
	err     error
}

func (f *fakeDockerCLI) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	key := strings.Join(args[:min(2, len(args))], ":")
	if err, ok := f.errs[key]; ok {
		return "", err
	}
	if f.err != nil {
		return "", f.err
	}
	return f.outputs[key], nil
}

// findCall returns the first recorded call starting with prefix, or nil.
func (f *fakeDockerCLI) findCall(prefix ...string) []string {
	for _, call := range f.calls {
		if len(call) >= len(prefix) && slices.Equal(call[:len(prefix)], prefix) {
			return call
		}
	}
	return nil
}

func TestFakeDockerCLIRecordsAndKeysOutputs(t *testing.T) {
	fake := &fakeDockerCLI{outputs: map[string]string{"image:inspect": "abc"}}
	out, err := fake.run(context.Background(), "image", "inspect", "tag")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "abc" {
		t.Errorf("output = %q, want abc", out)
	}
	if len(fake.calls) != 1 || strings.Join(fake.calls[0], " ") != "image inspect tag" {
		t.Errorf("calls = %v, want one [image inspect tag] call", fake.calls)
	}
}

func TestRealDockerCLIReportsCommandOnFailure(t *testing.T) {
	_, err := (&realDockerCLI{}).run(context.Background(), "info", "--bogus")
	if err == nil {
		t.Fatal("want error for an invalid docker invocation")
	}
	if !strings.Contains(err.Error(), "docker info --bogus") {
		t.Errorf("error %q does not name the command", err)
	}
}

// readyBuildkitClient substitutes a client that reports readiness immediately.
func readyBuildkitClient(t *testing.T) {
	t.Helper()
	restore := newBuildkitClient
	t.Cleanup(func() { newBuildkitClient = restore })
	newBuildkitClient = func(context.Context, string) (buildkitClient, error) {
		return &closeCountingClient{closeCalls: new(atomic.Int32)}, nil
	}
}

func TestDockerBackendEnsureStartsContainer(t *testing.T) {
	readyBuildkitClient(t)
	fake := &fakeDockerCLI{errs: map[string]error{"port:" + dockerBuilderContainer: errors.New("no such container")}}
	b := &dockerBackend{docker: fake}

	port, err := b.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if port <= 0 {
		t.Fatalf("port = %d, want > 0", port)
	}

	if fake.findCall("volume", "create") == nil {
		t.Error("no docker volume create call recorded")
	}
	runCall := fake.findCall("run", "-d")
	if runCall == nil {
		t.Fatal("no docker run -d call recorded")
	}
	for _, want := range []string{
		"--privileged",
		dockerBuilderContainer,
		builderImage,
		"--oci-worker-snapshotter",
		"native",
	} {
		if !slices.Contains(runCall, want) {
			t.Errorf("run args %v missing %q", runCall, want)
		}
	}
}

func TestDockerBackendEnsureReusesRunningContainer(t *testing.T) {
	fake := &fakeDockerCLI{outputs: map[string]string{"port:" + dockerBuilderContainer: "127.0.0.1:11234"}}
	b := &dockerBackend{docker: fake}

	port, err := b.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if port != 11234 {
		t.Errorf("port = %d, want 11234", port)
	}
	if fake.findCall("run", "-d") != nil {
		t.Errorf("unexpected run call: %v", fake.calls)
	}
}

func TestDockerBackendEnsureCachesPort(t *testing.T) {
	fake := &fakeDockerCLI{}
	b := &dockerBackend{docker: fake, port: 9999}

	port, err := b.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if port != 9999 {
		t.Errorf("port = %d, want 9999", port)
	}
	if len(fake.calls) != 0 {
		t.Errorf("expected no docker calls, got %v", fake.calls)
	}
}

func TestDockerBackendPruneNoContainerIsNoop(t *testing.T) {
	fake := &fakeDockerCLI{errs: map[string]error{"port:" + dockerBuilderContainer: errors.New("no such container")}}
	b := &dockerBackend{docker: fake}

	if err := b.PruneCache(context.Background()); err != nil {
		t.Fatalf("PruneCache: %v", err)
	}
	if fake.findCall("exec", dockerBuilderContainer) != nil {
		t.Errorf("unexpected exec call: %v", fake.calls)
	}
}

func TestDockerBackendPruneExecsWhenRunning(t *testing.T) {
	fake := &fakeDockerCLI{outputs: map[string]string{"port:" + dockerBuilderContainer: "127.0.0.1:11234"}}
	b := &dockerBackend{docker: fake}

	if err := b.PruneCache(context.Background()); err != nil {
		t.Fatalf("PruneCache: %v", err)
	}
	call := fake.findCall("exec", dockerBuilderContainer)
	if call == nil {
		t.Fatal("no docker exec call recorded")
	}
	joined := strings.Join(call, " ")
	for _, want := range []string{"buildctl", "prune", "--filter", "until=24h", "--all"} {
		if !strings.Contains(joined, want) {
			t.Errorf("exec args %q missing %q", joined, want)
		}
	}
}

func TestDockerBackendExportIsDockerTagged(t *testing.T) {
	spec := (&dockerBackend{}).Export("my/image:latest")
	if spec.Type != "docker" {
		t.Errorf("Type = %q, want docker", spec.Type)
	}
	if spec.Attrs["name"] != "my/image:latest" {
		t.Errorf("Attrs[name] = %q, want my/image:latest", spec.Attrs["name"])
	}
}

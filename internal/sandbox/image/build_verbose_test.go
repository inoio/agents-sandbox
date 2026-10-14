package image

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func verboseContains(ui *termio.Mock, substr string) bool {
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, substr) {
			return true
		}
	}
	return false
}

func TestEnsureImageVerboseNamesChangedInputs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "2.0.0")
	a := agentOpencode(t)
	oldLabels := computeImageIdentity(nil, "1.0.0", false).values()
	oldLabels[dockerfileIDLabelKey] = "stale"
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return msbImageConfig("sha256:existing", oldLabels, nil), nil
		},
	})
	fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	ui := &termio.Mock{}
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, ui); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !verboseContains(ui, "rebuilding runner image") || !verboseContains(ui, "agent version changed") {
		t.Errorf("verbose calls = %v, want the rebuild reason", ui.VerboseCalls)
	}
}

func TestEnsureImageVerboseReportsReuse(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(renderBytes(t, a, nil, false), "1.2.3"),
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return msbImageConfig("sha256:existing", labels, nil), nil
		},
	})
	ui := &termio.Mock{}
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, ui); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !verboseContains(ui, "reusing runner image") {
		t.Errorf("verbose calls = %v, want a reuse line", ui.VerboseCalls)
	}
}

func TestEnsureImageVerboseReportsForcedRebuild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	fakeBuilder(t, builder.BuildResult{ArchivePath: "/tmp/a", Digest: "sha256:new"}, nil)

	ui := &termio.Mock{}
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{Force: true}, ui); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !verboseContains(ui, "forced by --rebuild") {
		t.Errorf("verbose calls = %v, want a forced-rebuild reason", ui.VerboseCalls)
	}
}

func TestEnsureImageVerboseReportsBuildTimings(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	fakeBuilder(t, builder.BuildResult{
		ArchivePath: "/tmp/a",
		Digest:      "sha256:new",
		Timings:     builder.BuildTimings{Ensure: 36 * time.Second, Solve: 160 * time.Second, Total: 268 * time.Second},
		VertexTimings: []builder.VertexTiming{
			{
				Name:     "[agents-sandbox-runner 12/15] COPY --from=agents-sandbox-agent /opt/agents-sandbox /opt/agents-sandbox",
				Duration: 30 * time.Second,
				Step:     true,
			},
		},
	}, nil)

	ui := &termio.Mock{}
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{Force: true}, ui); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !verboseContains(ui, "Build timings") {
		t.Errorf("verbose calls = %v, want a build-timings line", ui.VerboseCalls)
	}
	if !verboseContains(ui, "COPY --from=agents-sandbox-agent") {
		t.Errorf("verbose calls = %v, want a per-step timing line", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsUpToDate(t *testing.T) {
	ui := &termio.Mock{}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return nil },
	}
	if err := EnsureLoaded(context.Background(), msbClient, "", "ref", ui); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "is up to date") {
		t.Errorf("verbose calls = %v, want an up-to-date line", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsArchiveLoad(t *testing.T) {
	ui := &termio.Mock{}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return errors.New("not cached") },
	}
	if err := EnsureLoaded(
		context.Background(), msbClient, newArchiveFile(t), "agents-sandbox/runner-p:opencode", ui,
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "from build archive") {
		t.Errorf("verbose calls = %v, want a build-archive load reason", ui.VerboseCalls)
	}
}

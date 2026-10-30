package volume

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPrefillSandboxNameIsPrefixFreeAndLabelled pins the task sandbox shape: no
// vendor prefix on the name, and the task label that makes it classifiable
// without one.
func TestPrefillSandboxNameIsPrefixFreeAndLabelled(t *testing.T) {
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{CreatedSandbox: &msb.MockSandbox{Name_: "prefill"}}
	vm := NewManager(&ui)

	err := vm.PrefillVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		&ui,
	)
	require.NoError(t, err)
	require.Len(t, client.CreatedSandboxCalls, 1)
	name := client.CreatedSandboxCalls[0].Name
	require.True(t, strings.HasPrefix(name, "myproject-opencode-"), "name = %q", name)
	require.NotContains(t, name, naming.LegacyTaskPrefix)

	var cfg msbSdk.SandboxConfig
	for _, opt := range client.CreatedSandboxCalls[0].Opts {
		opt(&cfg)
	}
	require.Equal(t, naming.SandboxTypeTask, cfg.Labels[naming.LabelType])
	require.Equal(t, "myproject", cfg.Labels[naming.LabelSlug])
	require.Equal(t, "opencode", cfg.Labels[naming.LabelAgent])
}

// TestTaskSandboxNameStaysWithinNameLimit pins the longest realistic task
// sandbox name (a long folder, the hyphenated `claude-code` agent, and a
// 19-digit nanos): it must fit the sandbox-name limit and round-trip to the same
// slug, agent and task flag, so it cannot be truncated into another artifact's
// namespace.
func TestTaskSandboxNameStaysWithinNameLimit(t *testing.T) {
	slug := strings.Repeat("a", 30) + "-1mjusbm3wikhb0"
	name := naming.TaskSandboxName(slug, "claude-code", 1729000000000000000)
	require.LessOrEqual(t, len(name), options.MaxSandboxNameLen, "name %q is too long", name)
	require.Equal(t, naming.ArtifactInfo{Slug: slug, Agent: "claude-code", Task: true}, naming.ParseVMName(name))
}

// TestCopySandboxNameIsPrefixFreeAndLabelled covers the second creation site.
func TestCopySandboxNameIsPrefixFreeAndLabelled(t *testing.T) {
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{CreatedSandbox: &msb.MockSandbox{Name_: "copy"}}
	vm := NewManager(&ui)

	err := vm.CopyVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"old-home-vol",
		"new-home-vol",
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		&ui,
	)
	require.NoError(t, err)
	require.Len(t, client.CreatedSandboxCalls, 1)
	name := client.CreatedSandboxCalls[0].Name
	require.True(t, strings.HasPrefix(name, "myproject-opencode-"), "name = %q", name)
	require.NotContains(t, name, naming.LegacyTaskPrefix)

	var cfg msbSdk.SandboxConfig
	for _, opt := range client.CreatedSandboxCalls[0].Opts {
		opt(&cfg)
	}
	require.Equal(t, naming.SandboxTypeTask, cfg.Labels[naming.LabelType])
	require.Equal(t, "myproject", cfg.Labels[naming.LabelSlug])
	require.Equal(t, "opencode", cfg.Labels[naming.LabelAgent])
}

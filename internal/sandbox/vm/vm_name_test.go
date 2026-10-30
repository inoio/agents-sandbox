package vm

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func TestProjectVMNameIsPrefixFreeAndTruncated(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode", projectVMName(k))
	require.Equal(t, "agents-sandbox-vm-myproj-1mjusbm3wikhb0-opencode", legacyProjectVMName(k))
	require.LessOrEqual(t,
		len(projectVMName(state.Key{Slug: strings.Repeat("a", 200), Agent: "opencode"})),
		options.MaxSandboxNameLen,
	)
}

func TestFindProjectVMFallsBackToLegacyName(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	legacy := &msb.MockSandboxHandle{
		Name_:   legacyProjectVMName(k),
		Status_: msbSdk.SandboxStatusRunning,
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == legacy.Name_ {
			return legacy, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}}

	handle, found, err := findProjectVM(context.Background(), client, k)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, legacy.Name_, handle.Name())
}

func TestFindProjectVMPrefersNewName(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	project := &msb.MockSandboxHandle{Name_: projectVMName(k), Status_: msbSdk.SandboxStatusRunning}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == project.Name_ {
			return project, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}}

	handle, found, err := findProjectVM(context.Background(), client, k)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, project.Name_, handle.Name())
}

func TestFindProjectVMNotFoundWhenNeitherExists(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}}

	handle, found, err := findProjectVM(context.Background(), client, k)
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, handle)
}

func TestLegacyProjectVMNameIsTruncatedWithPrefixKept(t *testing.T) {
	got := legacyProjectVMName(state.Key{Slug: strings.Repeat("a", 200), Agent: "opencode"})
	require.LessOrEqual(t, len(got), options.MaxSandboxNameLen)
	require.True(t, strings.HasPrefix(got, naming.LegacyVMPrefix))
	require.True(t, strings.HasSuffix(got, "-opencode"))
}

func TestFindProjectVMPropagatesLegacyProbeError(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == projectVMName(k) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
		}
		return nil, context.DeadlineExceeded
	}}

	_, _, err := findProjectVM(context.Background(), client, k)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestFindProjectVMSurfacesOtherErrors(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	client := &msb.MockMsbClient{}
	client.SetGetSandboxErr(context.DeadlineExceeded)

	_, _, err := findProjectVM(context.Background(), client, k)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSandboxIdentityClassifiesBothSchemesAndLabels(t *testing.T) {
	require.True(t, naming.SandboxIdentity("myproj-1mjusbm3wikhb0-opencode-1729000000000000000", nil).Task)
	require.True(t, naming.SandboxIdentity("agents-sandbox-task-x-1729", nil).Task)
	require.True(t, naming.SandboxIdentity("proj-aBc1234D", map[string]string{naming.LabelTask: "1"}).Task)
	require.True(
		t,
		naming.SandboxIdentity("proj-aBc1234D", map[string]string{naming.LabelType: naming.SandboxTypeTask}).Task,
	)
	require.False(t, naming.SandboxIdentity("myproj-1mjusbm3wikhb0-opencode", nil).Task)

	// The label is authoritative: a prefix-free project VM whose folder begins
	// with the legacy task prefix is not a task when labelled as a project.
	project := map[string]string{
		naming.LabelType:  naming.SandboxTypeProject,
		naming.LabelSlug:  "agents-sandbox-task-1mjusbm3wikhb0",
		naming.LabelAgent: "opencode",
	}
	got := naming.SandboxIdentity("agents-sandbox-task-1mjusbm3wikhb0-opencode", project)
	require.False(t, got.Task)
	require.Equal(t, "agents-sandbox-task-1mjusbm3wikhb0", got.Slug)
	require.Equal(t, "opencode", got.Agent)

	// The label carries the full slug even when the name was truncated.
	truncated := map[string]string{
		naming.LabelType:  naming.SandboxTypeProject,
		naming.LabelSlug:  "a-very-long-folder-name-1mjusbm3wikhb0",
		naming.LabelAgent: "opencode",
	}
	require.Equal(t, "a-very-long-folder-name-1mjusbm3wikhb0",
		naming.SandboxIdentity("agents-sandbox-vm-", truncated).Slug)
}

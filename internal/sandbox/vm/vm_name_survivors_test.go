package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// errVMNameBoom is a sentinel the sandbox lookup returns, so a test can prove
// the wrapping relationship survives.
var errVMNameBoom = errors.New("sandbox lookup boom")

// TestTruncateProjectVMNameSubtractsTheOverflow pins the exact truncated name:
// the slug loses exactly as many bytes as the name overflows the maximum, so the
// agent suffix is preserved. (Only a "<=" floor and a "-" that actually
// subtracts produce this; a "+" or a fixed slug length does not.)
func TestTruncateProjectVMNameSubtractsTheOverflow(t *testing.T) {
	got := truncateProjectVMName("", state.Key{Slug: strings.Repeat("a", 200), Agent: "opencode"})
	want := strings.Repeat("a", 119) + "-opencode"
	require.Equal(t, want, got)
	require.Len(t, got, options.MaxSandboxNameLen)
}

// TestTruncateProjectVMNameKeepsAtLeastZeroSlugBytes pins the max(..., 0)
// floor with a hostile input: when the overflow exceeds the whole slug, the slug
// is dropped entirely (a floor of 1 would keep one byte and shift the tail).
func TestTruncateProjectVMNameKeepsAtLeastZeroSlugBytes(t *testing.T) {
	got := truncateProjectVMName("", state.Key{Slug: "s", Agent: strings.Repeat("a", 130)})
	want := "-" + strings.Repeat("a", 127)
	require.Equal(t, want, got)
	require.Len(t, got, options.MaxSandboxNameLen)
}

// TestFindProjectVMPropagatesNewNameProbeError pins the first probe's failure
// handling: a non-not-found error under the new name aborts with an error naming
// that name and found=false, rather than falling through to the legacy probe.
func TestFindProjectVMPropagatesNewNameProbeError(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == projectVMName(k) {
			return nil, errVMNameBoom
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}}

	handle, found, err := findProjectVM(context.Background(), client, k)
	require.ErrorIs(t, err, errVMNameBoom)
	require.False(t, found)
	require.Nil(t, handle)
	require.Contains(t, err.Error(), projectVMName(k))
}

// TestFindProjectVMPropagatesLegacyProbeErrorFailsClosed pins the legacy probe's
// failure handling: a non-not-found error under the legacy name aborts with
// found=false and an error naming the legacy name.
func TestFindProjectVMPropagatesLegacyProbeErrorFailsClosed(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == projectVMName(k) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
		}
		return nil, errVMNameBoom
	}}

	handle, found, err := findProjectVM(context.Background(), client, k)
	require.ErrorIs(t, err, errVMNameBoom)
	require.False(t, found)
	require.Nil(t, handle)
	require.Contains(t, err.Error(), legacyProjectVMName(k))
}

package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func TestProjectVMNameIncludesAgent(t *testing.T) {
	got := ProjectVMName(state.Key{Slug: "myproject-aBc1234DeF", Agent: "pi"})
	want := "myproject-aBc1234DeF-pi"
	if got != want {
		t.Errorf("ProjectVMName = %q, want %q", got, want)
	}
	got = ProjectVMName(state.Key{Slug: "myproject-aBc1234DeF", Agent: "opencode"})
	want = "myproject-aBc1234DeF-opencode"
	if got != want {
		t.Errorf("ProjectVMName = %q, want %q", got, want)
	}
}

func TestProjectVMNameTruncationPreservesAgent(t *testing.T) {
	long := "x" + string(make([]byte, options.MaxSandboxNameLen))
	k := state.Key{Slug: long, Agent: "pi"}
	got := ProjectVMName(k)
	if len(got) > options.MaxSandboxNameLen {
		t.Fatalf("name %d bytes > max %d", len(got), options.MaxSandboxNameLen)
	}
	if !strings.HasSuffix(got, "-pi") {
		t.Errorf("truncated name %q lost the agent suffix", got)
	}
}

// TestProjectVMNameCutsAnOverlongAgentToTheMaximum pins that an agent name
// longer than the maximum name length still yields a name of exactly that
// length; the agent suffix may then be cut.
func TestProjectVMNameCutsAnOverlongAgentToTheMaximum(t *testing.T) {
	slug := "proj"
	agent := "a" + string(make([]byte, options.MaxSandboxNameLen))
	got := ProjectVMName(state.Key{Slug: slug, Agent: agent})
	if len(got) != options.MaxSandboxNameLen {
		t.Errorf("expected name cut to exactly %d bytes, got %d", options.MaxSandboxNameLen, len(got))
	}
}

func TestProjectVMNameIsPrefixFreeAndTruncated(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode", ProjectVMName(k))
	require.Equal(t, "agents-sandbox-vm-myproj-1mjusbm3wikhb0-opencode", legacyProjectVMName(k))
	require.LessOrEqual(t,
		len(ProjectVMName(state.Key{Slug: strings.Repeat("a", 200), Agent: "opencode"})),
		options.MaxSandboxNameLen,
	)
}

func TestLegacyProjectVMNameIsTruncatedWithPrefixKept(t *testing.T) {
	got := legacyProjectVMName(state.Key{Slug: strings.Repeat("a", 200), Agent: "opencode"})
	require.LessOrEqual(t, len(got), options.MaxSandboxNameLen)
	require.True(t, strings.HasPrefix(got, naming.LegacyVMPrefix))
	require.True(t, strings.HasSuffix(got, "-opencode"))
}

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

// TestFindProjectVM pins the lookup order: the project VM is found under its
// current name, else under its legacy prefixed name, else reported missing. A
// lookup that fails for another reason, under either name, fails closed with
// the cause and the name it probed.
func TestFindProjectVM(t *testing.T) {
	k := state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}
	current, legacy := ProjectVMName(k), legacyProjectVMName(k)
	cases := []struct {
		name      string
		sandboxes map[string]error // name -> lookup error; nil means the VM exists
		wantName  string
		wantErr   error
		wantProbe string
	}{
		{name: "current name", sandboxes: map[string]error{current: nil, legacy: nil}, wantName: current},
		{name: "legacy name", sandboxes: map[string]error{legacy: nil}, wantName: legacy},
		{name: "neither"},
		{
			name:      "current lookup fails",
			sandboxes: map[string]error{current: errVMNameBoom, legacy: nil},
			wantErr:   errVMNameBoom,
			wantProbe: current,
		},
		{
			name:      "legacy lookup fails",
			sandboxes: map[string]error{legacy: errVMNameBoom},
			wantErr:   errVMNameBoom,
			wantProbe: legacy,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
				lookupErr, exists := tc.sandboxes[name]
				switch {
				case !exists:
					return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
				case lookupErr != nil:
					return nil, lookupErr
				}
				return &msb.MockSandboxHandle{Name_: name}, nil
			}}

			handle, found, err := FindProjectVM(context.Background(), client, k)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Contains(t, err.Error(), tc.wantProbe)
				require.False(t, found)
				require.Nil(t, handle)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantName != "", found)
			if tc.wantName == "" {
				require.Nil(t, handle)
				return
			}
			require.Equal(t, tc.wantName, handle.Name())
		})
	}
}

// TestProjectVMHomeVolume pins the home volume read every caller relies on: a
// lookup error is returned, a missing VM or a nil handle counts as "no VM", and
// a mounts read failure reports the VM as existing so a destroying caller can
// fail closed.
func TestProjectVMHomeVolume(t *testing.T) {
	lookupErr := errors.New("lookup boom")
	mountsErr := errors.New("mounts boom")
	notFound := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}

	for _, tc := range []struct {
		name       string
		getSandbox func(context.Context, string) (msb.SandboxHandle, error)
		wantVolume string
		wantExists bool
		wantErr    error
	}{
		{
			name: "lookup error",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return nil, lookupErr
			},
			wantErr: lookupErr,
		},
		{
			name: "no VM",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return nil, notFound
			},
		},
		{
			name: "nil handle",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return nil, nil //nolint:nilnil // the nil handle is the case under test
			},
		},
		{
			name: "mounts unreadable",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return &msb.MockSandboxHandle{Name_: "vm", MountsErr: mountsErr}, nil
			},
			wantExists: true,
			wantErr:    mountsErr,
		},
		{
			name: "mounted home volume",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return &msb.MockSandboxHandle{Name_: "vm", Mounts: map[string]msb.MountConfig{
					mounts.VMHomeDir: msb.Mount.Named("home-vol", msb.MountOptions{}),
				}}, nil
			},
			wantVolume: "home-vol",
			wantExists: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &msb.MockMsbClient{GetSandboxFn: tc.getSandbox}

			volume, exists, err := ProjectVMHomeVolume(context.Background(), client, testVMKey())

			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if volume != tc.wantVolume || exists != tc.wantExists {
				t.Errorf("got (%q, %v), want (%q, %v)", volume, exists, tc.wantVolume, tc.wantExists)
			}
		})
	}
}

// errVMNameBoom is a sentinel the sandbox lookup returns, so a test can prove
// the wrapping relationship survives.
var errVMNameBoom = errors.New("sandbox lookup boom")

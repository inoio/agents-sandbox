package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestRecordImageProvenanceWarnsOnUnreadableState pins that a project state
// that cannot be read is a warning and nothing is recorded.
func TestRecordImageProvenanceWarnsOnUnreadableState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	// Make the state file resolve to a directory so os.ReadFile fails with a
	// non-not-exist error, forcing ReadState to return an error.
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	fs := msbtest.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("tool\n"),
	})
	ui := &termiotest.Mock{}
	sb := &msbtest.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, ui)
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "read state for transaction") {
		t.Errorf("expected a warning about the unreadable project state, got %v", ui.WarnCalls)
	}
}

// TestRecordImageProvenanceWarnsWhenStateCannotBeWritten pins that a project
// state that cannot be written is a warning, not a setup failure.
func TestRecordImageProvenanceWarnsWhenStateCannotBeWritten(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	// Persist an initial state first so the subsequent ReadState call succeeds,
	// then make the state directory read-only so the atomic write fails while
	// loading still works.
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatal(err)
	}
	dir := state.KeyDir(k)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot chmod state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	fs := msbtest.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("user\n"),
	})
	ui := &termiotest.Mock{}
	sb := &msbtest.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, ui)
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "could not persist project state") {
		t.Errorf("expected a warning about the failed persist, got %v", ui.WarnCalls)
	}
}

// TestRecordImageProvenancePreservesUnrelatedFields pins that recording the
// image's provenance changes only the agent and docker sources: every other
// field of the existing project state survives.
func TestRecordImageProvenancePreservesUnrelatedFields(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seed := state.HomeState{
		HomeVolume:      "vol",
		ImageDigest:     "d",
		AgentVersion:    "1.0.0",
		AgentSource:     state.AgentSourceTool,
		DockerSource:    state.AgentSourceTool,
		OfferedVersions: []string{"1.1.0"},
		EnvState:        state.EnvState{Hash: "e"},
		SecretState:     state.SecretState{Hash: "s"},
		NetworkState:    state.NetworkState{Hash: "n"},
		MountState:      state.MountState{Hash: "m"},
	}
	if err := seedHomeState(k, seed); err != nil {
		t.Fatalf("seed home state: %v", err)
	}
	sb := &msbtest.MockSandbox{FSValue_: msbtest.NewTestFS(map[string][]byte{
		agentSourcePath:  []byte(state.AgentSourceUser + "\n"),
		dockerSourcePath: []byte(state.AgentSourceUser + "\n"),
	})}

	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termiotest.Mock{})

	got, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	want := seed
	want.AgentSource = state.AgentSourceUser
	want.DockerSource = state.AgentSourceUser
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("provenance changed unrelated fields:\n got %+v\nwant %+v", *got, want)
	}
}

// TestRecordImageProvenanceDetectsToolAgentVersion pins that a tool-installed
// agent's version is detected and recorded with both sources.
func TestRecordImageProvenanceDetectsToolAgentVersion(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	fs := msbtest.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source":  []byte("tool\n"),
		"/etc/agents-sandbox/docker-source": []byte("user\n"),
	})
	sb := &msbtest.MockSandbox{
		FSValue_: fs,
		ShellOut: map[string]msb.ShellResult{
			"opencode --version": msbtest.NewTestResult(true, 0, "0.5.0\n", "", nil),
		},
	}
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termiotest.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentSource != state.AgentSourceTool || hs.DockerSource != state.AgentSourceUser ||
		hs.AgentVersion != "0.5.0" {
		t.Fatalf("provenance = %+v", hs)
	}
}

// TestRecordImageProvenanceSkipsMissingState pins that a first boot with no
// prior state.yaml writes nothing: the provenance write must not create a
// document with home_volume: "" (#430).
func TestRecordImageProvenanceSkipsMissingState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "freshproj", Agent: "opencode"}
	fs := msbtest.NewTestFS(map[string][]byte{
		agentSourcePath:  []byte(state.AgentSourceUser + "\n"),
		dockerSourcePath: []byte(state.AgentSourceUser + "\n"),
	})
	sb := &msbtest.MockSandbox{FSValue_: fs}

	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termiotest.Mock{})

	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
	if _, err := os.Stat(filepath.Join(state.KeyDir(k), "state.yaml")); !os.IsNotExist(err) {
		t.Errorf("state.yaml exists after provenance recording with no prior state: err = %v", err)
	}
}

// TestRecordImageProvenanceSkipsVersionForUserAgent pins that a user-provided
// agent's version is not recorded: the tool does not own it.
func TestRecordImageProvenanceSkipsVersionForUserAgent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	fs := msbtest.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("user\n"),
	})
	sb := &msbtest.MockSandbox{FSValue_: fs}
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termiotest.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentSource != state.AgentSourceUser {
		t.Errorf("AgentSource = %q, want user", hs.AgentSource)
	}
	if hs.AgentVersion != "" {
		t.Errorf("AgentVersion = %q, want empty for a user-provided agent", hs.AgentVersion)
	}
}

// TestRecordImageProvenanceReadErrorLeavesStateUntouched pins the pre-provenance
// skip: when the agent-source read fails the function must return before writing
// anything (so an existing provenance survives) and log the skip.
func TestRecordImageProvenanceReadErrorLeavesStateUntouched(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{AgentSource: state.AgentSourceUser}); err != nil {
		t.Fatal(err)
	}
	fs := msbtest.NewTestFS(nil) // every ReadString fails: no provenance files
	ui := termiotest.NewTestMock(t)
	recordImageProvenance(context.Background(), k, opencodeResolved(t), &msbtest.MockSandbox{FSValue_: fs}, &ui)

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentSource != state.AgentSourceUser {
		t.Errorf("AgentSource = %q, want the pre-existing %q (a read failure must not rewrite state)",
			hs.AgentSource, state.AgentSourceUser)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "no agent-source file in image") {
		t.Errorf("expected the pre-provenance verbose note, got %v", ui.VerboseCalls)
	}
}

// TestDetectToolAgentVersion pins that only an agent that reports its version
// has one detected, and that a version command that fails or prints no version
// is a warning with no version.
func TestDetectToolAgentVersion(t *testing.T) {
	cases := []struct {
		name     string
		agent    agent.Resolved
		sb       *msbtest.MockSandbox
		wantWarn string
	}{
		{name: "agent without a version command", agent: agent.Resolve(&fakeAgent{}), sb: &msbtest.MockSandbox{}},
		{
			name:     "version command fails",
			agent:    opencodeResolved(t),
			sb:       &msbtest.MockSandbox{ShellErr: errors.New("shell failed")},
			wantWarn: "could not detect agent version",
		},
		{
			name:  "unparsable version output",
			agent: opencodeResolved(t),
			sb: &msbtest.MockSandbox{ShellOut: map[string]msb.ShellResult{
				"opencode --version": msbtest.NewTestResult(true, 0, "not a version string", "", nil),
			}},
			wantWarn: "could not parse agent version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := &termiotest.Mock{}
			version, ok := detectToolAgentVersion(context.Background(), tc.agent, tc.sb, ui)
			if ok || version != "" {
				t.Errorf("detectToolAgentVersion = (%q, %v), want no version", version, ok)
			}
			warnings := strings.Join(ui.WarnCalls, "\n")
			if (tc.wantWarn == "") != (warnings == "") || !strings.Contains(warnings, tc.wantWarn) {
				t.Errorf("warnings = %v, want %q", ui.WarnCalls, tc.wantWarn)
			}
		})
	}
}

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
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestRecordImageProvenanceLoadError covers the branch where the project state
// cannot be read: a warning is emitted and recording is skipped.
func TestRecordImageProvenanceLoadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	// Make the state file resolve to a directory so os.ReadFile fails with a
	// non-not-exist error, forcing ReadState to return an error.
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("tool\n"),
	}, nil)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, ui)
	if !contains(joinStrings(ui.WarnCalls), "read state for transaction") {
		t.Errorf("expected a warning about the unreadable project state, got %v", ui.WarnCalls)
	}
}

// TestRecordImageProvenancePersistError covers the branch where persisting the
// project state fails after a successful read: a warning is emitted.
func TestRecordImageProvenancePersistError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
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

	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("user\n"),
	}, nil)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, ui)
	if !contains(joinStrings(ui.WarnCalls), "could not persist project state") {
		t.Errorf("expected a warning about the failed persist, got %v", ui.WarnCalls)
	}
}

// TestHomeWritersPreserveUnrelatedFields is a property guard over the boot
// provenance HomeState writer: each writer owns a small set of fields and must leave every
// other field of the existing document untouched. Each case seeds a fully
// populated state, runs one writer, and compares the result against the seed
// with only the writer's owned fields advanced.
func TestHomeWritersPreserveUnrelatedFields(t *testing.T) {
	seed := func() state.HomeState {
		return state.HomeState{
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
	}

	cases := []struct {
		name string
		run  func(t *testing.T, k state.Key)
		want func() state.HomeState
	}{
		{
			name: "recordImageProvenance",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				sb := &msb.MockSandbox{
					FSValue_: msb.NewTestFS(map[string][]byte{
						agentSourcePath:  []byte(state.AgentSourceUser + "\n"),
						dockerSourcePath: []byte(state.AgentSourceUser + "\n"),
					}, nil),
				}
				recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termio.Mock{})
			},
			want: func() state.HomeState {
				w := seed()
				w.AgentSource = state.AgentSourceUser
				w.DockerSource = state.AgentSourceUser
				return w
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			k := state.Key{Slug: "proj", Agent: "opencode"}
			if err := seedHomeState(k, seed()); err != nil {
				t.Fatalf("seed home state: %v", err)
			}
			tc.run(t, k)
			got, err := state.ReadState(k)
			if err != nil {
				t.Fatalf("ReadState: %v", err)
			}
			if want := tc.want(); !reflect.DeepEqual(*got, want) {
				t.Errorf("writer changed unrelated fields:\n got %+v\nwant %+v", *got, want)
			}
		})
	}
}

func TestRecordImageProvenanceDetectsToolAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source":  []byte("tool\n"),
		"/etc/agents-sandbox/docker-source": []byte("user\n"),
	}, nil)
	sb := &msb.MockSandbox{
		FSValue_: fs,
		ShellOut: map[string]msb.ShellResult{
			"opencode --version": msb.NewTestResult(true, 0, "0.5.0\n", "", nil),
		},
	}
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termio.Mock{})

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
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "freshproj", Agent: "opencode"}
	fs := msb.NewTestFS(map[string][]byte{
		agentSourcePath:  []byte(state.AgentSourceUser + "\n"),
		dockerSourcePath: []byte(state.AgentSourceUser + "\n"),
	}, nil)
	sb := &msb.MockSandbox{FSValue_: fs}

	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termio.Mock{})

	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
	if _, err := os.Stat(filepath.Join(state.KeyDir(k), "state.yaml")); !os.IsNotExist(err) {
		t.Errorf("state.yaml exists after provenance recording with no prior state: err = %v", err)
	}
}

func TestRecordImageProvenanceSkipsVersionForUserAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("user\n"),
	}, nil)
	sb := &msb.MockSandbox{FSValue_: fs}
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termio.Mock{})

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

func TestRecordImageProvenanceSkipsWhenFileAbsent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatal(err)
	}
	fs := msb.NewTestFS(nil, nil)
	recordImageProvenance(context.Background(), k, opencodeResolved(t), &msb.MockSandbox{FSValue_: fs}, &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentSource != "" || hs.AgentVersion != "" {
		t.Errorf("expected empty provenance for an image without the files, got %+v", hs)
	}
}

// TestRecordImageProvenancePreservesExistingFields verifies the write is a
// read-modify-write: the project's fingerprints and already-offered versions
// survive a provenance update.
func TestRecordImageProvenancePreservesExistingFields(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	existing := state.NewHomeState("home-vol", "digest")
	existing.OfferedVersions = []string{"0.4.0"}
	existing.EnvState = state.EnvState{Hash: "envhash"}
	if err := seedHomeState(k, existing); err != nil {
		t.Fatal(err)
	}

	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("tool\n"),
	}, nil)
	sb := &msb.MockSandbox{
		FSValue_: fs,
		ShellOut: map[string]msb.ShellResult{
			"opencode --version": msb.NewTestResult(true, 0, "0.5.0\n", "", nil),
		},
	}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.HomeVolume != "home-vol" || hs.ImageDigest != "digest" {
		t.Errorf("home/digest not preserved: %+v", hs)
	}
	if len(hs.OfferedVersions) != 1 || hs.OfferedVersions[0] != "0.4.0" {
		t.Errorf("offered versions not preserved: %+v", hs.OfferedVersions)
	}
	if hs.EnvState.Hash != "envhash" {
		t.Errorf("env fingerprint not preserved: %+v", hs.EnvState)
	}
	if hs.AgentSource != state.AgentSourceTool || hs.AgentVersion != "0.5.0" {
		t.Errorf("provenance not recorded: %+v", hs)
	}
}

// TestRecordImageProvenanceReadErrorLeavesStateUntouched pins the pre-provenance
// skip: when the agent-source read fails the function must return before writing
// anything (so an existing provenance survives) and log the skip.
func TestRecordImageProvenanceReadErrorLeavesStateUntouched(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{AgentSource: state.AgentSourceUser}); err != nil {
		t.Fatal(err)
	}
	fs := msb.NewTestFS(nil, nil) // every ReadString fails: no provenance files
	ui := termio.NewTestMock(t)
	recordImageProvenance(context.Background(), k, opencodeResolved(t), &msb.MockSandbox{FSValue_: fs}, &ui)

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

// TestDetectToolAgentVersionNotProvider covers the branch where the agent does
// not implement VersionProvider: no version is detected.
func TestDetectToolAgentVersionNotProvider(t *testing.T) {
	version, ok := detectToolAgentVersion(
		context.Background(),
		agent.Resolve(&fakeAgent{}),
		&msb.MockSandbox{},
		&termio.Mock{},
	)
	if ok || version != "" {
		t.Errorf("expected no detection, got (%q, %v)", version, ok)
	}
}

// TestDetectToolAgentVersionShellError covers the branch where detecting the
// agent version via Shell fails: a warning is emitted and no version is
// detected.
func TestDetectToolAgentVersionShellError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellErr: errors.New("shell failed")}
	version, ok := detectToolAgentVersion(context.Background(), opencodeResolved(t), sb, ui)
	if ok || version != "" {
		t.Errorf("expected no detection on shell error, got (%q, %v)", version, ok)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not detect agent version") {
		t.Errorf("expected a warning about the failed version detection, got %v", ui.WarnCalls)
	}
}

// TestDetectToolAgentVersionParseError covers the branch where the agent
// version output cannot be parsed: a warning is emitted and no version is
// detected.
func TestDetectToolAgentVersionParseError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"opencode --version": msb.NewTestResult(true, 0, "not a version string", "", nil),
		},
	}
	version, ok := detectToolAgentVersion(context.Background(), opencodeResolved(t), sb, ui)
	if ok || version != "" {
		t.Errorf("expected no detection on parse error, got (%q, %v)", version, ok)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not parse agent version") {
		t.Errorf("expected a warning about the parse failure, got %v", ui.WarnCalls)
	}
}

package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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
	recordImageProvenance(context.Background(), k, opencodeAgent(t), sb, &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentSource != agentSourceTool || hs.DockerSource != agentSourceUser || hs.AgentVersion != "0.5.0" {
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
		agentSourcePath:  []byte(agentSourceUser + "\n"),
		dockerSourcePath: []byte(agentSourceUser + "\n"),
	}, nil)
	sb := &msb.MockSandbox{FSValue_: fs}

	recordImageProvenance(context.Background(), k, opencodeAgent(t), sb, &termio.Mock{})

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
	recordImageProvenance(context.Background(), k, opencodeAgent(t), sb, &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentSource != agentSourceUser {
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
	recordImageProvenance(context.Background(), k, opencodeAgent(t), &msb.MockSandbox{FSValue_: fs}, &termio.Mock{})

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
	recordImageProvenance(context.Background(), k, opencodeAgent(t), sb, &termio.Mock{})

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
	if hs.AgentSource != agentSourceTool || hs.AgentVersion != "0.5.0" {
		t.Errorf("provenance not recorded: %+v", hs)
	}
}

// TestRecordImageProvenanceReadErrorLeavesStateUntouched pins the pre-provenance
// skip: when the agent-source read fails the function must return before writing
// anything (so an existing provenance survives) and log the skip.
func TestRecordImageProvenanceReadErrorLeavesStateUntouched(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{AgentSource: agentSourceUser}); err != nil {
		t.Fatal(err)
	}
	fs := msb.NewTestFS(nil, nil) // every ReadString fails: no provenance files
	ui := termio.NewTestMock(t)
	recordImageProvenance(context.Background(), k, opencodeAgent(t), &msb.MockSandbox{FSValue_: fs}, &ui)

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentSource != agentSourceUser {
		t.Errorf("AgentSource = %q, want the pre-existing %q (a read failure must not rewrite state)",
			hs.AgentSource, agentSourceUser)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "no agent-source file in image") {
		t.Errorf("expected the pre-provenance verbose note, got %v", ui.VerboseCalls)
	}
}

func TestCurrentAgentSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if got := currentAgentSource(k); got != "" {
		t.Errorf("currentAgentSource() = %q, want empty", got)
	}
	if err := seedHomeState(k, state.HomeState{AgentSource: agentSourceUser}); err != nil {
		t.Fatal(err)
	}
	if got := currentAgentSource(k); got != agentSourceUser {
		t.Errorf("currentAgentSource() = %q, want user", got)
	}
}

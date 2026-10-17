package vm

import (
	"context"
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
	recordImageProvenance(context.Background(), k, opencodeAgent(t), sb, &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentSource != agentSourceTool || hs.DockerSource != agentSourceUser || hs.AgentVersion != "0.5.0" {
		t.Fatalf("provenance = %+v", hs)
	}
}

func TestRecordImageProvenanceSkipsVersionForUserAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("user\n"),
	}, nil)
	sb := &msb.MockSandbox{FSValue_: fs}
	k := state.Key{Slug: "proj", Agent: "opencode"}
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
	if err := state.WriteState(k, state.NewHomeState("vol", "digest")); err != nil {
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
	if err := state.WriteState(k, existing); err != nil {
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

func TestCurrentAgentSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if got := currentAgentSource(opencodeAgent(t)); got != "" {
		t.Errorf("currentAgentSource() = %q, want empty", got)
	}
	if err := saveUpgradeState(upgradeState{AgentSource: agentSourceUser}); err != nil {
		t.Fatal(err)
	}
	if got := currentAgentSource(opencodeAgent(t)); got != agentSourceUser {
		t.Errorf("currentAgentSource() = %q, want user", got)
	}
}

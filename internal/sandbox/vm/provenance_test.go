package vm

import (
	"context"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestRecordImageProvenanceDetectsToolAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	fs := msb.NewTestFS(map[string][]byte{
		"/etc/opencode-sandbox/agent-source":  []byte("tool\n"),
		"/etc/opencode-sandbox/docker-source": []byte("user\n"),
	}, nil)
	sb := &msb.MockSandbox{
		FSValue_: fs,
		ShellOut: map[string]msb.ShellResult{
			"opencode --version": msb.NewTestResult(true, 0, "0.5.0\n", "", nil),
		},
	}
	recordImageProvenance(context.Background(), opencodeAgent(t), sb, &termio.Mock{})

	state, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	oc := state.Agents["opencode"]
	if oc.AgentSource != agentSourceTool {
		t.Errorf("AgentSource = %q, want tool", oc.AgentSource)
	}
	if oc.DockerSource != agentSourceUser {
		t.Errorf("DockerSource = %q, want user", oc.DockerSource)
	}
	if oc.CurrentVersion != "0.5.0" {
		t.Errorf("CurrentVersion = %q, want 0.5.0", oc.CurrentVersion)
	}
}

func TestRecordImageProvenanceSkipsVersionForUserAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	fs := msb.NewTestFS(map[string][]byte{
		"/etc/opencode-sandbox/agent-source": []byte("user\n"),
	}, nil)
	sb := &msb.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), opencodeAgent(t), sb, &termio.Mock{})

	state, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	oc := state.Agents["opencode"]
	if oc.AgentSource != agentSourceUser {
		t.Errorf("AgentSource = %q, want user", oc.AgentSource)
	}
	if oc.CurrentVersion != "" {
		t.Errorf("CurrentVersion = %q, want empty for a user-provided agent", oc.CurrentVersion)
	}
}

func TestRecordImageProvenanceSkipsWhenFileAbsent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	fs := msb.NewTestFS(nil, nil)
	recordImageProvenance(context.Background(), opencodeAgent(t), &msb.MockSandbox{FSValue_: fs}, &termio.Mock{})

	state, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	oc := state.Agents["opencode"]
	if oc.AgentSource != "" || oc.CurrentVersion != "" {
		t.Errorf("expected empty provenance for an image without the files, got %+v", oc)
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

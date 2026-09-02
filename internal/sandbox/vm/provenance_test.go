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
	if state.AgentSource != agentSourceTool {
		t.Errorf("AgentSource = %q, want tool", state.AgentSource)
	}
	if state.DockerSource != agentSourceUser {
		t.Errorf("DockerSource = %q, want user", state.DockerSource)
	}
	if state.CurrentVersion != "0.5.0" {
		t.Errorf("CurrentVersion = %q, want 0.5.0", state.CurrentVersion)
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
	if state.AgentSource != agentSourceUser {
		t.Errorf("AgentSource = %q, want user", state.AgentSource)
	}
	if state.CurrentVersion != "" {
		t.Errorf("CurrentVersion = %q, want empty for a user-provided agent", state.CurrentVersion)
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
	if state.AgentSource != "" || state.CurrentVersion != "" {
		t.Errorf("expected empty provenance for an image without the files, got %+v", state)
	}
}

func TestCurrentAgentSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if got := currentAgentSource(); got != "" {
		t.Errorf("currentAgentSource() = %q, want empty", got)
	}
	if err := saveUpgradeState(upgradeState{AgentSource: agentSourceUser}); err != nil {
		t.Fatal(err)
	}
	if got := currentAgentSource(); got != agentSourceUser {
		t.Errorf("currentAgentSource() = %q, want user", got)
	}
}

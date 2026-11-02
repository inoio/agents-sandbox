package vm

import (
	"context"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestEnsureProjectVMRecreateDestroysTheHandleItIsGiven pins the recreate
// target: the VM the decision was read from is destroyed, even when its name
// no longer resolves by the time ensure-vm runs.
func TestEnsureProjectVMRecreateDestroysTheHandleItIsGiven(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	existing := &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning}

	_, boot, err := EnsureProjectVM(
		context.Background(),
		&msb.MockMsbClient{},
		options.RunOptions{Agent: "opencode", Recreate: true},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		existing,
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if !existing.Destroyed {
		t.Error("recreate did not destroy the handle the decision was read from")
	}
	if boot != VMBootCreated {
		t.Errorf("boot = %v, want VMBootCreated", boot)
	}
}

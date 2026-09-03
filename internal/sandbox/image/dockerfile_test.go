package image

import (
	"os"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestResolveDockerfile_ProjectFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	content := "FROM custom/base:latest\n"
	testutil.WriteFile(t, configpaths.Get().ProjectConfigDir(), "Dockerfile", content)

	if got := string(ResolveDockerfile()); got != content {
		t.Errorf("ResolveDockerfile() = %q, want %q", got, content)
	}
}

func TestDockerfileFromImageSpecOpencode(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	out := DockerfileFromImageSpec(a.ImageSpec())
	s := string(out)
	for _, want := range []string{"ARG OPENCODE_VERSION", "org.opencode-sandbox.opencode-version", "OPENCODE_DISABLE_AUTOUPDATE", "curl -fsSL https://opencode.ai/install"} {
		if !strings.Contains(s, want) {
			t.Errorf("generated Dockerfile missing %q", want)
		}
	}
}

func TestResolveDockerfile_FallsBackToEmbedded(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	got := string(ResolveDockerfile())
	if got == "" {
		t.Error("ResolveDockerfile() = empty, want embedded fallback content")
	}
	if _, err := os.Stat(configpaths.Get().ProjectDockerfile()); !os.IsNotExist(err) {
		t.Fatalf("expected no project Dockerfile, but one exists at %s", configpaths.Get().ProjectDockerfile())
	}
	if got != string(embeddedDockerfile) {
		t.Errorf("ResolveDockerfile() = %q, want embedded %q", got, string(embeddedDockerfile))
	}
}

func TestResolveRunnerDockerfile_ProjectFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	content := "FROM custom/base:latest\n"
	testutil.WriteFile(t, configpaths.Get().ProjectConfigDir(), "Dockerfile", content)

	got := string(ResolveRunnerDockerfile(agentOpencode(t)))
	if got != content {
		t.Errorf("ResolveRunnerDockerfile() = %q, want %q", got, content)
	}
}

func TestResolveRunnerDockerfile_FallsBackToAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	got := string(ResolveRunnerDockerfile(agentOpencode(t)))
	if got == "" {
		t.Error("ResolveRunnerDockerfile() = empty, want agent-rendered fallback")
	}
	if !strings.Contains(got, "ARG OPENCODE_VERSION") {
		t.Error("fallback runner Dockerfile should be rendered for the opencode agent")
	}
}

func agentOpencode(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

func TestDockerfileFromImageSpecNPMAgents(t *testing.T) {
	for _, name := range []string{"pi", "claude-code", "opencode2"} {
		t.Run(name, func(t *testing.T) {
			a, ok := agent.Lookup(name)
			if !ok {
				t.Fatalf("%s agent not registered", name)
			}
			out := DockerfileFromImageSpec(a.ImageSpec())
			s := string(out)
			if !strings.Contains(s, "ARG "+a.ImageSpec().VersionArg) {
				t.Errorf("generated Dockerfile missing ARG %s", a.ImageSpec().VersionArg)
			}
			if !strings.Contains(s, a.ImageSpec().VersionLabel) {
				t.Errorf("generated Dockerfile missing version label %s", a.ImageSpec().VersionLabel)
			}
			if !strings.Contains(s, a.ImageSpec().InstallCommand) {
				t.Errorf("generated Dockerfile missing install command %q", a.ImageSpec().InstallCommand)
			}
		})
	}
}

func TestEmbeddedDockerfileInstallsNodeBeforeAgentBlock(t *testing.T) {
	s := string(embeddedDockerfile)
	nodeIdx := strings.Index(s, "nodesource")
	markerIdx := strings.Index(s, markerOpenCodeInstall)
	if nodeIdx < 0 {
		t.Fatal("embedded Dockerfile missing node install")
	}
	if markerIdx < 0 {
		t.Fatal("embedded Dockerfile missing agent install marker")
	}
	if nodeIdx > markerIdx {
		t.Error("node install must come before the agent install block so npm-based agents can install")
	}
}

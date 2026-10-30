package image

import (
	"bytes"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

// FuzzRenderDockerfile drives arbitrary user project-Dockerfile bytes through
// the composition step. The project Dockerfile is the one input to the runner
// image that the launcher does not author, so a panic here is a user-facing
// failure on their first `run`; and every successful render must produce a
// usable Dockerfile.
func FuzzRenderDockerfile(f *testing.F) {
	seeds := []string{
		"",
		"FROM debian:trixie-slim\nRUN echo hi\n",
		"FROM fedora:41\nRUN dnf install -y git\n",
		"FROM alpine AS build\nRUN make\n\nFROM debian:trixie-slim\nCOPY --from=build /out /out\n",
		"FROM debian:trixie-slim\n# agents-sandbox:docker\nRUN echo after\n",
		"# agents-sandbox:dind\n",
		"FROM\n",
		"FROM debian:trixie-slim\n\n\n",
		"FROM image WITH WEIRD case\nnot a dockerfile at all\n",
	}
	for _, seed := range seeds {
		f.Add(seed, false)
		f.Add(seed, true)
	}

	f.Fuzz(func(t *testing.T, projectDockerfile string, docker bool) {
		a, ok := agent.Lookup("opencode")
		if !ok {
			t.Fatal("agent opencode is not registered")
		}
		out, err := RenderDockerfile(
			a, "1.2.3", []byte(projectDockerfile), options.DockerSetting{Enabled: docker, Explicit: docker},
		)
		if err != nil {
			return
		}
		if len(out) == 0 {
			t.Fatalf(
				"RenderDockerfile(%q, docker=%v) reported no error but rendered nothing",
				projectDockerfile,
				docker,
			)
		}
		if !bytes.Contains(out, []byte("FROM ")) {
			t.Fatalf("RenderDockerfile(%q, docker=%v) rendered a Dockerfile with no FROM", projectDockerfile, docker)
		}
	})
}

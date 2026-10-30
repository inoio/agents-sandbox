package image

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPlanRunnerImageNoRebuildReusesChangedImage pins the --no-rebuild reuse: a
// present image whose inputs changed is reused, and the skipped reasons are
// still reported so the caller can name them.
func TestPlanRunnerImageNoRebuildReusesChangedImage(t *testing.T) {
	runnerAndBaseMock(t, seededRunnerLabels(t, "debian:trixie-slim@sha256:old"), "sha256:new")

	_, _, needsBuild, reasons, err := planRunnerImage(
		context.Background(), agentOpencode(t), nil, "proj", "1.2.3", "1.2.3",
		BuildOptions{Docker: dockerOff, NoRebuild: true},
	)
	if err != nil {
		t.Fatalf("planRunnerImage: %v", err)
	}
	if needsBuild {
		t.Error("needsBuild = true, want the changed image reused under --no-rebuild")
	}
	if !slices.Equal(reasons, []string{"base image updated"}) {
		t.Errorf("reasons = %v, want the skipped base-image reason", reasons)
	}
}

// TestPlanRunnerImageNoRebuildStillBuildsMissingImage pins that --no-rebuild
// only skips a rebuild: without an image there is nothing to reuse.
func TestPlanRunnerImageNoRebuildStillBuildsMissingImage(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("No such image")
		},
	})

	_, _, needsBuild, reasons, err := planRunnerImage(
		context.Background(), agentOpencode(t), nil, "proj", "1.2.3", "1.2.3",
		BuildOptions{Docker: dockerOff, NoRebuild: true},
	)
	if err != nil {
		t.Fatalf("planRunnerImage: %v", err)
	}
	if !needsBuild {
		t.Fatal("needsBuild = false, want a build when the runner image is absent")
	}
	if !slices.Equal(reasons, []string{"runner image not present locally"}) {
		t.Errorf("reasons = %v, want the not-present reason", reasons)
	}
}

// TestPreviewImageNoRebuildReportsReuseWithSkippedReasons pins that the dry run
// matches a real --no-rebuild run: reuse, with the skipped reasons.
func TestPreviewImageNoRebuildReportsReuseWithSkippedReasons(t *testing.T) {
	runnerAndBaseMock(t, seededRunnerLabels(t, "debian:trixie-slim@sha256:old"), "sha256:new")

	preview, err := previewRunnerImage(
		context.Background(), agentOpencode(t), nil, "proj",
		BuildOptions{AgentVersion: "1.2.3", Docker: dockerOff, NoRebuild: true}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("previewRunnerImage: %v", err)
	}
	if preview.NeedsBuild {
		t.Error("NeedsBuild = true, want reuse under --no-rebuild")
	}
	if !slices.Equal(preview.Reasons, []string{"base image updated"}) {
		t.Errorf("Reasons = %v, want the skipped base-image reason", preview.Reasons)
	}
}

func TestLogBuildDecisionNamesSkippedRebuildReasons(t *testing.T) {
	ui := &termio.Mock{}

	logBuildDecision(ui, "runner:tag", false, []string{"project Dockerfile changed", "agent version changed"},
		"opencode", "1.2.3", "")

	want := "reusing runner image runner:tag despite changes (--no-rebuild): " +
		"project Dockerfile changed; agent version changed"
	if !slices.Contains(ui.InfoCalls, want) {
		t.Errorf("InfoCalls = %v, want %q", ui.InfoCalls, want)
	}
}

func TestLogBuildDecisionReuseWithoutChangesStaysVerbose(t *testing.T) {
	ui := &termio.Mock{}

	logBuildDecision(ui, "runner:tag", false, nil, "opencode", "1.2.3", "")

	if len(ui.InfoCalls) != 0 {
		t.Errorf("InfoCalls = %v, want none for an unchanged image", ui.InfoCalls)
	}
	if !slices.ContainsFunc(ui.VerboseCalls, func(call string) bool {
		return strings.Contains(call, "content identity unchanged")
	}) {
		t.Errorf("VerboseCalls = %v, want the unchanged-identity line", ui.VerboseCalls)
	}
}

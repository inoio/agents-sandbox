package image

import (
	"context"
	"errors"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

func TestExistingAgentVersionReadsImageLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withInspectLabels(t, map[string]string{agentVersionLabelKey: "1.4.0"}, true)

	got, ok := ExistingAgentVersion(context.Background(), docker.Get(), "proj", "opencode")
	if !ok || got != "1.4.0" {
		t.Fatalf("ExistingAgentVersion() = %q,%v want 1.4.0,true", got, ok)
	}
}

func TestExistingAgentVersionRejectsSentinels(t *testing.T) {
	for _, sentinel := range []string{"", identityNone, userProvidedImageIdentity} {
		t.Run("sentinel "+sentinel, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			withInspectLabels(t, map[string]string{agentVersionLabelKey: sentinel}, true)

			if got, ok := ExistingAgentVersion(context.Background(), docker.Get(), "proj", "opencode"); ok {
				t.Fatalf("ExistingAgentVersion() = %q,%v want false for sentinel %q", got, ok, sentinel)
			}
		})
	}
}

func TestExistingAgentVersionAbsentImage(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withInspectLabels(t, nil, false)

	if got, ok := ExistingAgentVersion(context.Background(), docker.Get(), "proj", "opencode"); ok {
		t.Fatalf("ExistingAgentVersion() = %q,%v want false for absent image", got, ok)
	}
}

func TestExistingAgentVersionMissingLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withInspectLabels(t, map[string]string{"unrelated": "x"}, true)

	if got, ok := ExistingAgentVersion(context.Background(), docker.Get(), "proj", "opencode"); ok {
		t.Fatalf("ExistingAgentVersion() = %q,%v want false for missing label", got, ok)
	}
}

func TestBuildReasonsRebuildsWhenLocalBaseImageChanged(t *testing.T) {
	built := computeImageIdentity(nil, "1.2.3", false)
	built.BaseImage = "debian:trixie-slim@sha256:old"
	desired := built
	desired.BaseImage = "debian:trixie-slim@sha256:new"
	state := runnerImageState{Present: true, DockerfileID: "abc", Labels: identityLabels(built)}

	needsBuild, reasons := buildReasons(state, "abc", desired)

	if !needsBuild {
		t.Fatal("expected a build when the local base image digest differs from the baked one")
	}
	if len(reasons) != 1 || reasons[0] != "base image updated" {
		t.Errorf("reasons = %v, want only the base-image reason", reasons)
	}
}

func TestBuildReasonsIgnoresUnresolvedLocalBaseImage(t *testing.T) {
	built := computeImageIdentity(nil, "1.2.3", false)
	built.BaseImage = "debian:trixie-slim@sha256:old"
	desired := built
	desired.BaseImage = ""
	state := runnerImageState{Present: true, DockerfileID: "abc", Labels: identityLabels(built)}

	if needsBuild, reasons := buildReasons(state, "abc", desired); needsBuild {
		t.Errorf("expected no build when the base image is not present locally, got reasons %v", reasons)
	}
}

func TestBuildReasonsIgnoresBaseImageForImageWithoutBaseLabel(t *testing.T) {
	built := computeImageIdentity(nil, "1.2.3", false)
	labels := identityLabels(built)
	delete(labels, baseLabelKey)
	desired := built
	desired.BaseImage = "debian:trixie-slim@sha256:new"
	state := runnerImageState{Present: true, DockerfileID: "abc", Labels: labels}

	if needsBuild, reasons := buildReasons(state, "abc", desired); needsBuild {
		t.Errorf("expected no build for an image without a base label, got reasons %v", reasons)
	}
}

func TestBuildReasonsSkipsWhenDockerfileIDMatches(t *testing.T) {
	id := computeImageIdentity([]byte("FROM ubuntu:24.04\n"), "1.2.3", false)
	state := runnerImageState{Present: true, DockerfileID: "abc", Labels: identityLabels(id)}
	needsBuild, reasons := buildReasons(state, "abc", id)
	if needsBuild {
		t.Error("expected no build when the dockerfile-id matches")
	}
	if reasons != nil {
		t.Errorf("reasons = %v, want nil", reasons)
	}
}

func TestBuildReasonsMissingImage(t *testing.T) {
	needsBuild, reasons := buildReasons(runnerImageState{}, "abc", imageIdentity{})
	if !needsBuild {
		t.Fatal("expected a build when the runner image is absent")
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "not present") {
		t.Errorf("reasons = %v, want a not-present reason", reasons)
	}
}

func TestBuildReasonsNamesChangedComponents(t *testing.T) {
	existing := identityLabels(computeImageIdentity(nil, "", false))
	desired := computeImageIdentity([]byte("FROM ubuntu:24.04\n"), "2.0.0", true)
	state := runnerImageState{Present: true, DockerfileID: "old", Labels: existing}
	needsBuild, reasons := buildReasons(state, "new", desired)
	if !needsBuild {
		t.Fatal("expected a build when the identity differs")
	}
	joined := strings.Join(reasons, "|")
	for _, want := range []string{"project Dockerfile changed", "agent version changed", "docker-in-docker changed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("reasons %v missing %q", reasons, want)
		}
	}
}

func TestBuildReasonsFallsBackForLegacyImage(t *testing.T) {
	state := runnerImageState{
		Present:      true,
		DockerfileID: "old",
		Labels:       map[string]string{dockerfileIDLabelKey: "old"},
	}
	needsBuild, reasons := buildReasons(state, "new", imageIdentity{})
	if !needsBuild {
		t.Fatal("expected a build when the identity differs")
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "content changed") {
		t.Errorf("reasons = %v, want a generic content-change reason", reasons)
	}
}

func TestBuildReasonsRebuildsWhenHostUserChanged(t *testing.T) {
	built := computeImageIdentity(nil, "1.2.3", false)
	built.User = hostUserIdentity(1000, 1000)
	desired := built
	desired.User = hostUserIdentity(1001, 1001)
	state := runnerImageState{Present: true, DockerfileID: "abc", Labels: identityLabels(built)}

	needsBuild, reasons := buildReasons(state, "abc", desired)

	if !needsBuild {
		t.Fatal("expected a build when the host uid/gid differs from the baked dev user")
	}
	if len(reasons) != 1 || reasons[0] != "host user uid/gid changed" {
		t.Errorf("reasons = %v, want only the host-user reason", reasons)
	}
}

func TestBuildReasonsIgnoresHostUserForImageWithoutUserLabel(t *testing.T) {
	built := computeImageIdentity(nil, "1.2.3", false)
	labels := identityLabels(built)
	delete(labels, userLabelKey)
	desired := built
	desired.User = hostUserIdentity(1001, 1001)
	state := runnerImageState{Present: true, DockerfileID: "abc", Labels: labels}

	if needsBuild, reasons := buildReasons(state, "abc", desired); needsBuild {
		t.Errorf("expected no build for an image that predates the user label, got reasons %v", reasons)
	}
}

func TestBuildReasonsCombinesSourceAndHostChanges(t *testing.T) {
	built := computeImageIdentity(nil, "1.2.3", false)
	built.User = hostUserIdentity(1000, 1000)
	desired := computeImageIdentity(nil, "2.0.0", false)
	desired.User = hostUserIdentity(1001, 1001)
	state := runnerImageState{Present: true, DockerfileID: "old", Labels: identityLabels(built)}

	_, reasons := buildReasons(state, "new", desired)

	want := []string{"agent version changed", "host user uid/gid changed"}
	if strings.Join(reasons, "|") != strings.Join(want, "|") {
		t.Errorf("reasons = %v, want %v", reasons, want)
	}
}

func TestReadImageInfoFromDockerNilConfig(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{}}, nil
		},
	})
	env, err := readImageInfoFromDocker(context.Background(), docker.Get(), "runner-tag")
	if err != nil {
		t.Fatalf("readImageInfoFromDocker: %v", err)
	}
	if env != nil {
		t.Errorf("env = %v, want nil when the image has no config", env)
	}
}

func withInspectLabels(t *testing.T, labels map[string]string, present bool) {
	t.Helper()
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if !present {
				return client.ImageInspectResult{}, errors.New("no such image")
			}
			return client.ImageInspectResult{Config: &dockerspec.DockerOCIImageConfig{Labels: labels}}, nil
		},
	})
}

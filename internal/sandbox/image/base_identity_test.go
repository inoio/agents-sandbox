package image

import (
	"context"
	"errors"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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

// runnerAndBaseMock serves the runner image under its tag with runnerLabels and
// the distro base under its ref with baseID (an empty baseID means the base is
// absent). Any pull fails the test: the build decision must stay local.
func runnerAndBaseMock(t *testing.T, runnerLabels map[string]string, baseID string) {
	t.Helper()
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == distroBaseRef {
				if baseID == "" {
					return client.ImageInspectResult{}, errors.New("No such image")
				}
				return client.ImageInspectResult{ID: baseID}, nil
			}
			return client.ImageInspectResult{Config: &dockerspec.DockerOCIImageConfig{Labels: runnerLabels}}, nil
		},
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			t.Errorf("unexpected pull of %s during the build decision", ref)
			return nil, errors.New("unexpected pull")
		},
	})
}

// seededRunnerLabels returns the labels of a runner image built from the
// current plan with the given base provenance label.
func seededRunnerLabels(t *testing.T, baseLabel string) map[string]string {
	t.Helper()
	plan, err := renderBuildPlan(agentOpencode(t), nil, dockerOff, "1.2.3", "1.2.3")
	if err != nil {
		t.Fatalf("renderBuildPlan: %v", err)
	}
	identity := plan.identity
	identity.BaseImage = baseLabel
	labels := identityLabels(identity)
	labels[dockerfileIDLabelKey] = plan.dockerfileID
	return labels
}

func TestPlanRunnerImageRebuildsWhenLocalBaseAdvanced(t *testing.T) {
	runnerAndBaseMock(t, seededRunnerLabels(t, "debian:trixie-slim@sha256:old"), "sha256:new")

	_, _, needsBuild, reasons, err := planRunnerImage(
		context.Background(), agentOpencode(t), nil, "proj", dockerOff, "1.2.3", "1.2.3", false,
	)
	if err != nil {
		t.Fatalf("planRunnerImage: %v", err)
	}
	if !needsBuild || strings.Join(reasons, "|") != "base image updated" {
		t.Errorf("needsBuild = %v, reasons = %v, want a rebuild for the updated base", needsBuild, reasons)
	}
}

func TestPlanRunnerImageReusesImageWhenBaseUnchangedOrAbsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		baseID string
	}{
		{"base unchanged", "sha256:same"},
		{"base absent locally", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runnerAndBaseMock(t, seededRunnerLabels(t, "debian:trixie-slim@sha256:same"), tc.baseID)

			_, _, needsBuild, reasons, err := planRunnerImage(
				context.Background(), agentOpencode(t), nil, "proj", dockerOff, "1.2.3", "1.2.3", false,
			)
			if err != nil {
				t.Fatalf("planRunnerImage: %v", err)
			}
			if needsBuild {
				t.Errorf("expected the image to be reused, got reasons %v", reasons)
			}
		})
	}
}

// TestResolveBaseDigestRefreshPullsPresentBase pins that --rebuild refreshes the
// base from its registry even when a copy is cached locally.
func TestResolveBaseDigestRefreshPullsPresentBase(t *testing.T) {
	pulled := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if pulled {
				return client.ImageInspectResult{ID: "sha256:fresh"}, nil
			}
			return client.ImageInspectResult{ID: "sha256:stale"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = true
			return docker.PullResponse{Body: strings.NewReader("")}, nil
		},
	})

	ui := &termio.Mock{}

	got, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", true, ui)
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != "debian:trixie-slim@sha256:fresh" {
		t.Errorf("resolveBaseDigest = %q, want the freshly pulled digest", got)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none after a successful refresh", ui.WarnCalls)
	}
}

// TestResolveBaseDigestRefreshFallsBackToLocalBase pins that a failed refresh
// (offline, registry down) keeps building from the cached base with a warning.
func TestResolveBaseDigestRefreshFallsBackToLocalBase(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:cached"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, errors.New("dial tcp: no route to host")
		},
	})
	ui := &termio.Mock{}

	got, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", true, ui)
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != "debian:trixie-slim@sha256:cached" {
		t.Errorf("resolveBaseDigest = %q, want the cached digest", got)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "no route to host") {
		t.Errorf("warnings = %v, want one naming the refresh failure", ui.WarnCalls)
	}
}

func TestResolveBaseDigestRefreshFailsWithoutLocalBase(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("No such image")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, errors.New("pull access denied")
		},
	})

	_, err := resolveBaseDigest(context.Background(), "private/base:latest", true, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "pull base image") {
		t.Errorf("resolveBaseDigest error = %v, want the pull failure", err)
	}
}

func TestResolveBaseDigestUsesLocalBaseWithoutPulling(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:cached"}, nil
		},
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			t.Errorf("unexpected pull of %s for a cached base without refresh", ref)
			return nil, errors.New("unexpected pull")
		},
	})

	got, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", false, &termio.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != "debian:trixie-slim@sha256:cached" {
		t.Errorf("resolveBaseDigest = %q, want the cached digest", got)
	}
}

func TestResolveBaseDigestLogsPullOfAbsentBase(t *testing.T) {
	pulled := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if !pulled {
				return client.ImageInspectResult{}, errors.New("No such image")
			}
			return client.ImageInspectResult{ID: "sha256:pulled"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = true
			return docker.PullResponse{Body: strings.NewReader("")}, nil
		},
	})
	ui := &termio.Mock{}

	if _, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", false, ui); err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if len(ui.VerboseCalls) != 1 || !strings.Contains(ui.VerboseCalls[0], "not present locally, pulling") {
		t.Errorf("verbose lines = %v, want the pull notice", ui.VerboseCalls)
	}
}

func TestLocalBaseImageTreatsUnknownImageAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result client.ImageInspectResult
		err    error
		want   string
	}{
		{"present", client.ImageInspectResult{ID: "sha256:cached"}, nil, "debian:trixie-slim@sha256:cached"},
		{"inspect error", client.ImageInspectResult{ID: "sha256:cached"}, errors.New("boom"), ""},
		{"empty image id", client.ImageInspectResult{}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					return tc.result, tc.err
				},
			})
			if got := localBaseImage(context.Background(), "debian:trixie-slim"); got != tc.want {
				t.Errorf("localBaseImage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveBaseDigestWrapsCauses(t *testing.T) {
	cause := errors.New("cause")
	for _, tc := range []struct {
		name    string
		pullErr error
		waitErr error
	}{
		{"pull call fails", cause, nil},
		{"pull stream fails", nil, cause},
		{"inspect after pull fails", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, cause
				},
				ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
					if tc.pullErr != nil {
						return nil, tc.pullErr
					}
					return docker.PullResponse{Body: strings.NewReader(""), WaitErr: tc.waitErr}, nil
				},
			})
			_, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", false, &termio.Mock{})
			if !errors.Is(err, cause) {
				t.Errorf("resolveBaseDigest error = %v, want it to wrap the cause", err)
			}
		})
	}
}

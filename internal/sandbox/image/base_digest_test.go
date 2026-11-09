package image

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestResolveBaseDigestRefreshPullsPresentBase pins that --rebuild refreshes the
// base from its registry even when a copy is cached locally.
func TestResolveBaseDigestRefreshPullsPresentBase(t *testing.T) {
	pulled := false
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if pulled {
				return client.ImageInspectResult{ID: "sha256:fresh"}, nil
			}
			return client.ImageInspectResult{ID: "sha256:stale"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = true
			return dockertest.PullResponse{Body: strings.NewReader("")}, nil
		},
	})

	ui := &termiotest.Mock{}

	got, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", true, ui)
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
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:cached"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, errors.New("dial tcp: no route to host")
		},
	})
	ui := &termiotest.Mock{}

	got, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", true, ui)
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
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("No such image")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, errors.New("pull access denied")
		},
	})

	_, err := resolveBaseDigest(context.Background(), docker.Get(), "private/base:latest", true, &termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), "pull base image") {
		t.Errorf("resolveBaseDigest error = %v, want the pull failure", err)
	}
}

func TestResolveBaseDigestUsesLocalBaseWithoutPulling(t *testing.T) {
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:cached"}, nil
		},
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			t.Errorf("unexpected pull of %s for a cached base without refresh", ref)
			return nil, errors.New("unexpected pull")
		},
	})

	got, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", false, &termiotest.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != "debian:trixie-slim@sha256:cached" {
		t.Errorf("resolveBaseDigest = %q, want the cached digest", got)
	}
}

func TestResolveBaseDigestLogsPullOfAbsentBase(t *testing.T) {
	pulled := false
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if !pulled {
				return client.ImageInspectResult{}, errors.New("No such image")
			}
			return client.ImageInspectResult{ID: "sha256:pulled"}, nil
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = true
			return dockertest.PullResponse{Body: strings.NewReader("")}, nil
		},
	})
	ui := &termiotest.Mock{}

	if _, err := resolveBaseDigest(context.Background(), docker.Get(), "debian:trixie-slim", false, ui); err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if len(ui.VerboseCalls) != 1 || !strings.Contains(ui.VerboseCalls[0], "not present locally, pulling") {
		t.Errorf("verbose lines = %v, want the pull notice", ui.VerboseCalls)
	}
}

// TestResolveBaseDigestWrapsPullAndInspectFailures pins that each failure on the
// pull path names the step that failed and wraps its cause.
func TestResolveBaseDigestWrapsPullAndInspectFailures(t *testing.T) {
	cause := errors.New("cause")
	for _, tc := range []struct {
		name        string
		pullErr     error
		waitErr     error
		wantMessage string
	}{
		{"pull call fails", cause, nil, "pull base image"},
		{"pull stream fails", nil, cause, "pull base image"},
		{"inspect after pull fails", nil, nil, "inspect pulled base image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, cause
				},
				ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
					if tc.pullErr != nil {
						return nil, tc.pullErr
					}
					return dockertest.PullResponse{Body: strings.NewReader(""), WaitErr: tc.waitErr}, nil
				},
			})
			_, err := resolveBaseDigest(
				context.Background(),
				docker.Get(),
				"debian:trixie-slim",
				false,
				&termiotest.Mock{},
			)
			if !errors.Is(err, cause) {
				t.Errorf("resolveBaseDigest error = %v, want it to wrap the cause", err)
			}
			if err != nil && !strings.Contains(err.Error(), tc.wantMessage) {
				t.Errorf("resolveBaseDigest error = %v, want it to name %q", err, tc.wantMessage)
			}
		})
	}
}

// TestResolveBaseDigestPullsAbsentBase verifies the pull-then-inspect path: the
// reference is pulled as given (the caller passes the qualified base ref), while
// the returned provenance label keeps the familiar spelling.
func TestResolveBaseDigestPullsAbsentBase(t *testing.T) {
	var pulled string
	inspects := 0
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			inspects++
			if inspects == 1 {
				return client.ImageInspectResult{}, errors.New("not found")
			}
			return client.ImageInspectResult{ID: "sha256:pulled"}, nil
		},
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = ref
			return dockertest.PullResponse{Body: strings.NewReader("")}, nil
		},
	})
	const qualified = "docker.io/library/debian:trixie-slim"
	got, err := resolveBaseDigest(context.Background(), docker.Get(), qualified, false, &termiotest.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if pulled != qualified {
		t.Errorf("ImagePull called with %q, want %q", pulled, qualified)
	}
	if got != "debian:trixie-slim@sha256:pulled" {
		t.Errorf("resolveBaseDigest = %q, want the familiar spelling in the label", got)
	}
}

// TestResolveBaseDigestSurfacesPullFailure pins that a registry-level pull
// failure is surfaced as the classified base-image error with its troubleshooting
// link, not swallowed and re-reported as a misleading post-pull inspect failure.
// Both delivery paths are covered: the pull call's own error and a registry
// failure inside the stream. The test also asserts a surfaced failure leaves no
// second inspect to confuse the message.
func TestResolveBaseDigestSurfacesPullFailure(t *testing.T) {
	const pullFailure = `pull access denied for private/base, repository does not exist or may require 'docker login'`
	for _, tc := range []struct {
		name string
		pull func() (client.ImagePullResponse, error)
	}{
		{
			name: "failure delivered inside the stream",
			pull: func() (client.ImagePullResponse, error) {
				return dockertest.PullResponse{WaitErr: errors.New(pullFailure)}, nil
			},
		},
		{
			name: "failure returned by the pull call",
			pull: func() (client.ImagePullResponse, error) {
				return nil, errors.New(pullFailure)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspects := 0
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					inspects++
					return client.ImageInspectResult{}, errors.New("No such image")
				},
				ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
					return tc.pull()
				},
			})
			_, err := resolveBaseDigest(
				context.Background(),
				docker.Get(),
				"private/base:latest",
				false,
				&termiotest.Mock{},
			)
			if err == nil || !strings.Contains(err.Error(), "base image not found or not logged in") {
				t.Errorf("resolveBaseDigest error = %v, want the classified base-image failure", err)
			}
			if err != nil && !strings.Contains(err.Error(), "the-base-image-cannot-be-found") {
				t.Errorf("resolveBaseDigest error = %v, want the troubleshooting docs link", err)
			}
			if inspects != 1 {
				t.Errorf("ImageInspect called %d times, want 1 (a surfaced pull failure must not re-inspect)", inspects)
			}
		})
	}
}

// TestResolveBaseDigestKeepsUnparseableRef covers a project Dockerfile FROM that
// is not a valid image reference: the recorded label carries it through
// unchanged rather than dropping the base it could not parse.
func TestResolveBaseDigestKeepsUnparseableRef(t *testing.T) {
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:odd"}, nil
		},
	})
	const unparseable = "Not A Valid Ref"
	got, err := resolveBaseDigest(context.Background(), docker.Get(), unparseable, false, &termiotest.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != unparseable+"@sha256:odd" {
		t.Errorf("resolveBaseDigest = %q, want the ref carried through unchanged", got)
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
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					return tc.result, tc.err
				},
			})
			if got := localBaseImage(context.Background(), docker.Get(), "debian:trixie-slim"); got != tc.want {
				t.Errorf("localBaseImage = %q, want %q", got, tc.want)
			}
		})
	}
}

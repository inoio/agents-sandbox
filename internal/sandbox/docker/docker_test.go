package docker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestCheckDockerAPIReturnsTrueWhenPingSucceeds(t *testing.T) {
	testUI := testutil.NewTestio(t)

	WithDockerMock(t, &MockDockerClient{
		PingFn: func(ctx context.Context, opts client.PingOptions) (client.PingResult, error) {
			return client.PingResult{APIVersion: "1.44"}, nil
		},
	})

	if !CheckDockerAPI(context.Background(), &testUI) {
		t.Fatal("expected CheckDockerAPI to return true when ping succeeds")
	}
}

func TestCheckDockerAPIReturnsFalseWhenPingFails(t *testing.T) {
	testUI := testutil.NewTestio(t)

	WithDockerMock(t, &MockDockerClient{
		PingFn: func(ctx context.Context, opts client.PingOptions) (client.PingResult, error) {
			return client.PingResult{}, errors.New("connection refused")
		},
	})

	if CheckDockerAPI(context.Background(), &testUI) {
		t.Fatal("expected CheckDockerAPI to return false when ping fails")
	}
	var out []string
	for _, e := range testUI.ErrorCalls {
		out = append(out, e.Msg)
	}
	outStr := strings.Join(out, " ")
	if !strings.Contains(outStr, "Docker API unreachable") {
		t.Errorf("expected log to contain 'Docker API unreachable', got %q", outStr)
	}
	if !strings.Contains(outStr, "connection refused") {
		t.Errorf("expected log to contain the underlying error, got %q", outStr)
	}
}

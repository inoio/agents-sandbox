package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestDockerfileTarContainsDockerfile(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	tarBuf, err := dockerfileTar(dockerfile)
	if err != nil {
		t.Fatalf("dockerfileTar failed: %v", err)
	}

	tr := tar.NewReader(tarBuf)
	header, err := tr.Next()
	if err != nil {
		t.Fatalf("unexpected error reading tar: %v", err)
	}
	if header.Name != "Dockerfile" {
		t.Errorf("expected tar entry 'Dockerfile', got %q", header.Name)
	}
	content, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("unexpected error reading tar content: %v", err)
	}
	if !bytes.Equal(content, dockerfile) {
		t.Errorf("tar content does not match dockerfile")
	}
}

func TestCheckDockerAPIReturnsTrueWhenPingSucceeds(t *testing.T) {
	testUI := testutil.TermUIMock(t)

	WithDockerMock(t, &MockDockerClient{
		PingFn: func(_ context.Context, _ client.PingOptions) (client.PingResult, error) {
			return client.PingResult{APIVersion: "1.44"}, nil
		},
	})

	if !CheckDockerAPI(context.Background(), &testUI) {
		t.Fatal("expected CheckDockerAPI to return true when ping succeeds")
	}
}

func TestCheckDockerAPIReturnsFalseWhenPingFails(t *testing.T) {
	testUI := testutil.TermUIMock(t)

	WithDockerMock(t, &MockDockerClient{
		PingFn: func(_ context.Context, _ client.PingOptions) (client.PingResult, error) {
			return client.PingResult{}, errors.New("connection refused")
		},
	})

	if CheckDockerAPI(context.Background(), &testUI) {
		t.Fatal("expected CheckDockerAPI to return false when ping fails")
	}
	var errorMsgs []string
	for _, e := range testUI.ErrorCalls {
		errorMsgs = append(errorMsgs, e.Msg)
	}
	outStr := strings.Join(errorMsgs, " ")
	if !strings.Contains(outStr, "Docker API unreachable") {
		t.Errorf("expected log to contain 'Docker API unreachable', got %q", outStr)
	}
	if !strings.Contains(outStr, "connection refused") {
		t.Errorf("expected log to contain the underlying error, got %q", outStr)
	}
}

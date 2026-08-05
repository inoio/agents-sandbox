package docker

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
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

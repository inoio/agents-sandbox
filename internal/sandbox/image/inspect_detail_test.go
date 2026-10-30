package image

import (
	"context"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestInspectImagePopulatesDetail(t *testing.T) {
	size := int64(2 * 1024 * 1024 * 1024)
	layerSize := int64(1024)
	erofsSize := int64(2048)
	mock := &msb.MockMsbClient{ImageInspectDetailFn: func(_ context.Context, _ string) (*msb.ImageDetail, error) {
		return &msb.ImageDetail{
			Reference:      "agents-sandbox/runner-proj:latest",
			ManifestDigest: "sha256:abc123",
			Architecture:   "arm64",
			OS:             "linux",
			SizeBytes:      &size,
			CreatedAt:      time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
			Config: msbSdk.ImageConfig{
				Env:        []string{"A=1", "B=2"},
				Cmd:        []string{"/bin/bash"},
				Entrypoint: []string{"/entry"},
				WorkingDir: "/workspace",
				User:       "dev",
				Labels:     map[string]string{"k": "v"},
				StopSignal: "SIGTERM",
			},
			Layers: []msbSdk.ImageLayer{
				{
					Position:            0,
					DiffID:              "sha256:layer0",
					BlobDigest:          "sha256:blob0",
					MediaType:           "application/vnd.oci.image.layer.v1.tar+gzip",
					CompressedSizeBytes: &layerSize,
					ErofsSizeBytes:      &erofsSize,
				},
			},
		}, nil
	}}
	msb.WithMsbMock(t, mock)

	got, err := InspectImage(context.Background(), "agents-sandbox/runner-proj:latest")
	if err != nil {
		t.Fatalf("InspectImage() error = %v", err)
	}
	if got.Reference != "agents-sandbox/runner-proj:latest" || got.Digest != "sha256:abc123" ||
		got.Architecture != "arm64" || got.OS != "linux" ||
		got.Size != "2 GiB" || got.CreatedAt != "2026-08-17 10:42:36" {
		t.Errorf("InspectImage() identity = %+v", got)
	}
	if got.WorkingDir != "/workspace" || got.User != "dev" || got.StopSignal != "SIGTERM" ||
		len(got.Env) != 2 || len(got.Cmd) != 1 || len(got.Entrypoint) != 1 ||
		got.Labels["k"] != "v" {
		t.Errorf("InspectImage() config = %+v", got)
	}
	if len(got.Layers) != 1 {
		t.Fatalf("InspectImage() layers = %+v, want 1", got.Layers)
	}
	layer := got.Layers[0]
	if layer.Position != 0 || layer.DiffID != "sha256:layer0" || layer.BlobDigest != "sha256:blob0" ||
		layer.CompressedSize != "1 KiB" || layer.ErofsSize != "2 KiB" {
		t.Errorf("InspectImage() layer = %+v", layer)
	}
}

func TestInspectImageUnknownLayerSize(t *testing.T) {
	mock := &msb.MockMsbClient{ImageInspectDetailFn: func(_ context.Context, _ string) (*msb.ImageDetail, error) {
		return &msb.ImageDetail{
			Reference: "agents-sandbox/runner-x",
			Layers:    []msbSdk.ImageLayer{{Position: 0, DiffID: "sha256:l"}},
		}, nil
	}}
	msb.WithMsbMock(t, mock)

	got, err := InspectImage(context.Background(), "agents-sandbox/runner-x")
	if err != nil {
		t.Fatalf("InspectImage() error = %v", err)
	}
	if got.Size != unknownSize || got.Layers[0].CompressedSize != unknownSize ||
		got.Layers[0].ErofsSize != unknownSize {
		t.Errorf("InspectImage() unknown sizes = %+v", got)
	}
}

func TestInspectImageMissing(t *testing.T) {
	msb.WithMsbMock(
		t,
		&msb.MockMsbClient{ImageInspectDetailFn: func(_ context.Context, _ string) (*msb.ImageDetail, error) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrImageNotFound, Message: "missing"}
		}},
	)
	_, err := InspectImage(context.Background(), "agents-sandbox/runner-missing")
	if err == nil || !strings.Contains(err.Error(), "inspect image") {
		t.Fatalf("InspectImage() error = %v, want inspect image error", err)
	}
}

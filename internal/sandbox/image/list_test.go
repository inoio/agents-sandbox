package image

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
)

func TestListImagesFiltersByImagePrefix(t *testing.T) {
	mockClient := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			msb.MockImageHandle{
				Reference_:      "opencode-msb/runner-proj-aBc1234D:3k5q07ywpibwp5",
				ManifestDigest_: "sha256:abc123",
			},
			msb.MockImageHandle{Reference_: "opencode-msb/runner-other-de456:def789", ManifestDigest_: "sha256:def789"},
			msb.MockImageHandle{Reference_: "python:3.12", ManifestDigest_: "sha256:python123"},
			msb.MockImageHandle{Reference_: "ubuntu:24.04", ManifestDigest_: "sha256:ubuntu24"},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListImages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []Info{
		{Reference: "opencode-msb/runner-proj-aBc1234D:3k5q07ywpibwp5", Digest: "sha256:abc123"},
		{Reference: "opencode-msb/runner-other-de456:def789", Digest: "sha256:def789"},
	}
	if len(result) != len(expected) {
		t.Fatalf("expected %d images, got %d", len(expected), len(result))
	}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("ListImages() mismatch\n  got:  %+v\n  want: %+v", result, expected)
	}
}

func TestListImagesReturnsPrefixOnly_NoPrefixedImages(t *testing.T) {
	mockClient := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			msb.MockImageHandle{Reference_: "python:3.12", ManifestDigest_: "sha256:python123"},
			msb.MockImageHandle{Reference_: "nginx:latest", ManifestDigest_: "sha256:nginx001"},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListImages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 images, got %d", len(result))
	}
}

func TestListImagesReturnsErrorFromMsB(t *testing.T) {
	wantMsg := "connection refused"
	mockClient := &msb.MockMsbClient{
		ListImagesErr: errors.New(wantMsg),
	}
	msb.WithMsbMock(t, mockClient)

	_, err := ListImages(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "list images:") {
		t.Errorf("expected error to contain 'list images:', got %v", err)
	}
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("expected error to contain %q, got %v", wantMsg, err)
	}
}

func TestListImagesEmptyReturnsNil(t *testing.T) {
	mockClient := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mockClient)

	result, err := ListImages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty list, got %+v", result)
	}
}

func TestListImagesPrefixMatchesNamingImagePrefix(t *testing.T) {
	prefix := naming.ImagePrefix
	if prefix == "" {
		t.Fatal("naming.ImagePrefix is empty, cannot test")
	}

	mockClient := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			msb.MockImageHandle{Reference_: prefix + "runner-foo:latest", ManifestDigest_: "sha256:foo"},
			msb.MockImageHandle{Reference_: "notopencodemsb-bar:1.0", ManifestDigest_: "sha256:bar"},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListImages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 image, got %d", len(result))
	}
	if result[0].Reference != prefix+"runner-foo:latest" {
		t.Errorf("unexpected reference: %s", result[0].Reference)
	}
}

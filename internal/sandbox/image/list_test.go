package image

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestListImagesIncludesBothSchemes(t *testing.T) {
	mockClient := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			msb.MockImageHandle{
				Reference_:      "agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest",
				ManifestDigest_: "sha256:abc123",
				SizeBytes_:      int64Ptr(1024 * 1024 * 1024),
				CreatedAt_:      time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
			},
			msb.MockImageHandle{
				Reference_:      "otherproj-1mjusbm3wikhb0:opencode-latest",
				ManifestDigest_: "sha256:def789",
			},
			msb.MockImageHandle{
				Reference_:      "docker.io/library/foo:1",
				ManifestDigest_: "sha256:foreign",
			},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListImages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []Info{
		{
			Reference: "agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest",
			Digest:    "sha256:abc123",
			Size:      "1 GiB",
			CreatedAt: "2026-08-17 10:42:36",
		},
		{
			Reference: "otherproj-1mjusbm3wikhb0:opencode-latest",
			Digest:    "sha256:def789",
			Size:      unknownSize,
			CreatedAt: "",
		},
	}
	if len(result) != len(expected) {
		t.Fatalf("expected %d images, got %d (%+v)", len(expected), len(result), result)
	}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("ListImages() mismatch\n  got:  %+v\n  want: %+v", result, expected)
	}
}

func TestListImagesExcludesRepositoryRefs(t *testing.T) {
	mockClient := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			msb.MockImageHandle{Reference_: "docker.io/library/foo:1", ManifestDigest_: "sha256:foo"},
			msb.MockImageHandle{Reference_: "ghcr.io/acme/app:v2", ManifestDigest_: "sha256:app"},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListImages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 images, got %+v", result)
	}
}

// TestListImagesOmitsSingleSegmentRefsWithoutHash pins the tightened ownership
// filter: a single-segment reference without the launcher's slug hash (a
// manually loaded image) is not reported, while a hash-bearing runner image is.
func TestListImagesOmitsSingleSegmentRefsWithoutHash(t *testing.T) {
	mockClient := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			msb.MockImageHandle{Reference_: "manualimage:opencode-latest", ManifestDigest_: "sha256:manual"},
			msb.MockImageHandle{
				Reference_:      "agents-sandbox/runner-manualimage:latest",
				ManifestDigest_: "sha256:legacy-manual",
			},
			msb.MockImageHandle{Reference_: "proj-1mjusbm3wikhb0:opencode-latest", ManifestDigest_: "sha256:owned"},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListImages(context.Background())
	if err != nil {
		t.Fatalf("ListImages() = %v", err)
	}
	want := []Info{{
		Reference: "proj-1mjusbm3wikhb0:opencode-latest",
		Digest:    "sha256:owned",
		Size:      unknownSize,
		CreatedAt: "",
	}}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ListImages() = %+v, want only the hash-bearing runner image %+v", result, want)
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

func int64Ptr(n int64) *int64 { return new(n) }

func TestImageSize(t *testing.T) {
	tests := []struct {
		name string
		in   *int64
		want string
	}{
		{"nil", nil, unknownSize},
		{"negative", int64Ptr(-1), unknownSize},
		{"zero", int64Ptr(0), "0 B"},
		{"bytes", int64Ptr(512), "512 B"},
		{"kib", int64Ptr(1024), "1 KiB"},
		{"gib", int64Ptr(2 * 1024 * 1024 * 1024), "2 GiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageSize(tt.in); got != tt.want {
				t.Errorf("imageSize(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

package image

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// testDockerImageID is a full sha256 image ID whose 16-char prefix keys the
// content-addressed reference in tests.
const testDockerImageID = "sha256:9f2c1a7b4d6e0f3199999999999999999999999999999999999999999999999999"

// testContentIdentity is the structured identity of the image the reuse tests
// describe: project "test-project", agent opencode, content id 9f2c1a7b4d6e0f31.
func testContentIdentity() naming.ImageIdentity {
	return naming.ImageIdentity{Slug: "test-project", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31"}
}

func TestImageContentIDTrimsAndTruncates(t *testing.T) {
	if got := imageContentID(testDockerImageID); got != "9f2c1a7b4d6e0f31" {
		t.Errorf("imageContentID(%q) = %q, want the sha256-prefix-free 16-char prefix", testDockerImageID, got)
	}
	if got := imageContentID(""); got != "" {
		t.Errorf("imageContentID(\"\") = %q, want empty", got)
	}
	if got := imageContentID(
		"sha256:9F2C1A7B4D6E0F3199999999999999999999999999999999999999999999999999",
	); got != "9f2c1a7b4d6e0f31" {
		t.Errorf("imageContentID(uppercase id) = %q, want the lowercased 16-char prefix", got)
	}
	// A 17-character id pins the exclusive bound: it must still truncate to 16.
	if got := imageContentID("sha256:0123456789abcdefg"); got != "0123456789abcdef" {
		t.Errorf("imageContentID(17-char id) = %q, want the 16-char prefix", got)
	}
}

func TestImageRefCandidates(t *testing.T) {
	const slug = "proj-1mjusbm3wikhb0"
	contentID := "9f2c1a7b4d6e0f31"
	contentRef := naming.ContentImageRef(slug, "opencode", contentID)
	dockerTag := naming.DockerImageRef(slug, "opencode")
	nameRef := naming.RunnerImageRef(slug, "opencode")
	identity := naming.ImageIdentity{Slug: slug, Agent: "opencode", ContentID: contentID}

	cases := []struct {
		name string
		info ImageInfo
		want []string
	}{
		{
			"content, name, and legacy refs in preference order",
			ImageInfo{Ref: contentRef, Identity: identity, DockerTag: dockerTag},
			[]string{contentRef, nameRef, dockerTag},
		},
		{
			"the identity supplies the name ref without a Docker tag",
			ImageInfo{Ref: contentRef, Identity: identity},
			[]string{contentRef, nameRef},
		},
		{
			"empty ref is dropped",
			ImageInfo{Ref: "", Identity: identity, DockerTag: dockerTag},
			[]string{nameRef, dockerTag},
		},
		{
			"duplicate refs are deduplicated",
			ImageInfo{Ref: nameRef, Identity: identity, DockerTag: nameRef},
			[]string{nameRef},
		},
		{
			"a foreign docker tag alone contributes no name ref",
			ImageInfo{Ref: "", DockerTag: "docker.io/library/foreign:1"},
			[]string{"docker.io/library/foreign:1"},
		},
	}
	for _, tc := range cases {
		if got := imageRefCandidates(tc.info); !slices.Equal(got, tc.want) {
			t.Errorf("%s: imageRefCandidates() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestEnsureImageWithClientSetsContentRef pins that the built image's content id
// keys the microsandbox reference, while the Docker tag stays the slug/agent name.
func TestEnsureImageWithClientSetsContentRef(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID:     testDockerImageID,
				Config: dockerConfigWith(testAgentVersion, nil),
			}, nil
		},
	})

	info, err := EnsureImageWithClient(
		context.Background(),
		a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"myproj-1abc1234defg5",
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if want := "myproj-1abc1234defg5:opencode.9f2c1a7b4d6e0f31"; info.Ref != want {
		t.Errorf("info.Ref = %q, want the content ref %q", info.Ref, want)
	}
	if want := naming.DockerImageRef("myproj-1abc1234defg5", "opencode"); info.DockerTag != want {
		t.Errorf("info.DockerTag = %q, want the unchanged Docker tag %q", info.DockerTag, want)
	}
	if want := (naming.ImageIdentity{
		Slug: "myproj-1abc1234defg5", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31",
	}); info.Identity != want {
		t.Errorf("info.Identity = %+v, want the structured identity %+v", info.Identity, want)
	}
}

// TestEnsureImageWithClientFallsBackToNameRefWhenContentIDEmpty pins the
// no-content-id fallback: an image whose inspect reports no ID keeps the
// prefix-free name reference rather than emitting a dangling content suffix.
func TestEnsureImageWithClientFallsBackToNameRefWhenContentIDEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{Config: dockerConfigWith("1.2.3", nil)}, nil
		},
	})

	info, err := EnsureImageWithClient(
		context.Background(),
		a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"myproj-1abc1234defg5",
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if want := naming.RunnerImageRef("myproj-1abc1234defg5", "opencode"); info.Ref != want {
		t.Errorf("info.Ref = %q, want the name ref %q", info.Ref, want)
	}
	if want := (naming.ImageIdentity{Slug: "myproj-1abc1234defg5", Agent: "opencode"}); info.Identity != want {
		t.Errorf("info.Identity = %+v, want the identity with no content id %+v", info.Identity, want)
	}
}

// TestEnsureLoadedReusesContentRef pins the primary reuse path: when the cache
// already holds the content under the content reference, nothing is reimported.
func TestEnsureLoadedReusesContentRef(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == contentRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the content ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != contentRef {
		t.Errorf("info.Ref = %q, want the reused content ref %q", info.Ref, contentRef)
	}
}

// TestEnsureLoadedReusesNameRefForMigration pins that a 0.6.0 install, which
// cached the image under the prefix-free name reference, keeps its cache: the
// name ref is derived from the image's structured identity and reused when its
// content matches.
func TestEnsureLoadedReusesNameRefForMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	nameRef := naming.RunnerImageRef("test-project", "opencode")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == nameRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the name ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != nameRef {
		t.Errorf("info.Ref = %q, want the reused name ref %q", info.Ref, nameRef)
	}
}

// TestEnsureLoadedReusesNameRefFromIdentity pins that the name-ref candidate
// comes from the image's structured identity rather than from parsing the
// Docker tag: an info that names its (slug, agent, contentID) reuses the
// cached name reference even when no Docker tag accompanies it.
func TestEnsureLoadedReusesNameRefFromIdentity(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	nameRef := naming.RunnerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == nameRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the identity's name ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != nameRef {
		t.Errorf("info.Ref = %q, want the reused name ref %q", info.Ref, nameRef)
	}
}

// TestEnsureLoadedReusesLegacyPrefixedRefForMigration pins that a pre-0.6
// install, which cached the image under the prefixed Docker tag, keeps its
// cache: the Docker-tag candidate is reused when its content matches.
func TestEnsureLoadedReusesLegacyPrefixedRefForMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == dockerTag {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the prefixed ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != dockerTag {
		t.Errorf("info.Ref = %q, want the reused prefixed ref %q", info.Ref, dockerTag)
	}
}

// TestEnsureLoadedLoadsUnderContentRefWhenNoCandidateMatches pins the fresh-load
// path: with no candidate cached, the Docker image is loaded under the content
// reference.
func TestEnsureLoadedLoadsUnderContentRefWhenNoCandidateMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	var savedRefs []string
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, refs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			savedRefs = refs
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	ui := &termio.Mock{}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:new"},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(savedRefs) != 1 || savedRefs[0] != dockerTag {
		t.Errorf("ImageSave refs = %v, want the Docker tag %q", savedRefs, dockerTag)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != contentRef {
		t.Errorf("LoadedImages = %v, want the content ref %q", msbClient.LoadedImages, contentRef)
	}
	if info.Ref != contentRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, contentRef)
	}
}

// TestEnsureLoadedLoadsUnderContentRefWhenContentDiffers pins that a cached
// candidate holding stale content is not reused: the image loads under the
// content ref, and the load reason reports the content mismatch.
func TestEnsureLoadedLoadsUnderContentRefWhenContentDiffers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == dockerTag {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:old"}, nil
		},
	}
	ui := &termio.Mock{}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:new"},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != contentRef {
		t.Errorf("LoadedImages = %v, want the content ref %q", msbClient.LoadedImages, contentRef)
	}
	if info.Ref != contentRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, contentRef)
	}
}

// TestEnsureLoadedFallsBackToDockerTagWhenRefEmpty pins the legacy-only
// resolution: an ImageInfo built before the ref was resolved carries only the
// Docker tag, so loading falls back to it as the microsandbox reference.
func TestEnsureLoadedFallsBackToDockerTagWhenRefEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: "", Digest: "sha256:new"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if info.Ref != legacyRef {
		t.Errorf("info.Ref = %q, want the Docker tag %q", info.Ref, legacyRef)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != legacyRef {
		t.Errorf("LoadedImages = %v, want [%s]", msbClient.LoadedImages, legacyRef)
	}
}

// TestEnsureLoadedReusesLegacyRefWhenContentMatches pins the no-reimport rule:
// when a pre-change install already cached the image under the legacy reference
// with the same content, that reference is reused instead of loading a second
// copy under the prefix-free reference.
func TestEnsureLoadedReusesLegacyRefWhenContentMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the legacy ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != legacyRef {
		t.Errorf("info.Ref = %q, want the reused legacy ref %q", info.Ref, legacyRef)
	}
}

// TestEnsureLoadedLoadsUnderNewRefWhenLegacyAbsent pins that a fresh install
// loads the image under the prefix-free reference.
func TestEnsureLoadedLoadsUnderNewRefWhenLegacyAbsent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:new"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != newRef {
		t.Errorf("LoadedImages = %v, want [%s]", msbClient.LoadedImages, newRef)
	}
	if info.Ref != newRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, newRef)
	}
}

// TestEnsureLoadedLoadsUnderNewRefWhenLegacyContentDiffers pins that a legacy
// reference holding stale content is not reused: the current Docker image is
// loaded under the prefix-free reference instead.
func TestEnsureLoadedLoadsUnderNewRefWhenLegacyContentDiffers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:old"}, nil
		},
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:new"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != newRef {
		t.Errorf("LoadedImages = %v, want [%s]", msbClient.LoadedImages, newRef)
	}
	if info.Ref != newRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, newRef)
	}
}

package image

import (
	"cmp"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

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

func TestEnsureLoadedVerboseReportsUpToDate(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:same", Config: dockerConfigWith("", nil)}, nil
		},
	})
	ui := &termiotest.Mock{}
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:same"}, nil
		},
	}
	if _, err := EnsureLoaded(
		context.Background(),
		docker.Get(),
		msbClient,
		ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:same"},
		ui,
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "is up to date") {
		t.Errorf("verbose calls = %v, want an up-to-date line", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsNotPresent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	ui := &termiotest.Mock{}
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: "agents-sandbox/runner-p:opencode", Ref: "agents-sandbox/runner-p:opencode"}, ui,
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "not present in microsandbox cache") {
		t.Errorf("verbose calls = %v, want a not-present reason", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsStaleCache(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:docker-new", Config: dockerConfigWith("", nil)}, nil
		},
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	ui := &termiotest.Mock{}
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:msb-old"}, nil
		},
	}
	if _, err := EnsureLoaded(
		context.Background(),
		docker.Get(),
		msbClient,
		ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:docker-new"},
		ui,
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "cached content differs from the Docker image") {
		t.Errorf("verbose calls = %v, want a stale-cache reason", ui.VerboseCalls)
	}
}

// TestEnsureLoadedFailsWhenTheImageTransferFails pins that a failed Docker save
// or microsandbox load is reported, and that a failed save loads nothing.
func TestEnsureLoadedFailsWhenTheImageTransferFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		saveErr error
		loadErr error
	}{
		{name: "docker save fails", saveErr: errors.New("docker save failed")},
		{name: "microsandbox load fails", loadErr: errors.New("load boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			rTag := runnerTag("test-project", agentOpencode(t).Name())
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
				ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
					if tc.saveErr != nil {
						return nil, tc.saveErr
					}
					return io.NopCloser(strings.NewReader("tar")), nil
				},
			})
			msbClient := &msbtest.MockMsbClient{
				ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
				ImageLoadFn: func(_ context.Context, _ string, _ io.Reader) error {
					return tc.loadErr
				},
			}
			info := ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:new"}
			_, err := EnsureLoaded(context.Background(), docker.Get(), msbClient, info, &termiotest.Mock{})
			if want := cmp.Or(tc.saveErr, tc.loadErr); !errors.Is(err, want) {
				t.Errorf("EnsureLoaded error = %v, want it to wrap %v", err, want)
			}
			if tc.saveErr != nil && len(msbClient.LoadedImages) != 0 {
				t.Errorf("expected no load on save failure, got %d", len(msbClient.LoadedImages))
			}
		})
	}
}

// TestEnsureLoadedDecision pins the load decision table: the content digest is
// authoritative, and a cached image that records no digest is unknown content
// that must be reloaded. The dockerfile-id label is never trusted as a
// substitute for the digest, and host Docker is never inspected.
func TestEnsureLoadedDecision(t *testing.T) {
	cases := []struct {
		name          string
		cached        bool
		msbDigest     string
		msbLabels     map[string]string
		info          ImageInfo
		wantLoadCount int
	}{
		{
			name:          "not cached loads",
			cached:        false,
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			wantLoadCount: 1,
		},
		{
			name:          "equal digest skips",
			cached:        true,
			msbDigest:     "sha256:new",
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			wantLoadCount: 0,
		},
		{
			name:          "different digest loads",
			cached:        true,
			msbDigest:     "sha256:old",
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			wantLoadCount: 1,
		},
		{
			name:          "no digest must reload even when the label matches",
			cached:        true,
			msbLabels:     map[string]string{dockerfileIDLabelKey: "abc"},
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			wantLoadCount: 1,
		},
		{
			name:          "no digest, different label loads",
			cached:        true,
			msbLabels:     map[string]string{dockerfileIDLabelKey: "old"},
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			wantLoadCount: 1,
		},
		{
			name:          "different digest with equal label loads",
			cached:        true,
			msbDigest:     "sha256:old",
			msbLabels:     map[string]string{dockerfileIDLabelKey: "abc"},
			info:          ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			wantLoadCount: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			hostInspected := false
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
				ImageInspectFn: func(
					_ context.Context, _ string, _ ...client.ImageInspectOption,
				) (client.ImageInspectResult, error) {
					hostInspected = true
					return client.ImageInspectResult{}, nil
				},
				ImageSaveFn: func(
					_ context.Context, _ []string, _ ...client.ImageSaveOption,
				) (client.ImageSaveResult, error) {
					return io.NopCloser(strings.NewReader("tar-data")), nil
				},
			})
			msbClient := &msbtest.MockMsbClient{
				ImageGetFn: func(_ context.Context, _ string) error {
					if tc.cached {
						return nil
					}
					return errors.New("not cached")
				},
				ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
					return &msb.ImageConfig{Digest: tc.msbDigest, Labels: tc.msbLabels}, nil
				},
			}
			if _, err := EnsureLoaded(
				context.Background(),
				docker.Get(),
				msbClient,
				tc.info,
				&termiotest.Mock{},
			); err != nil {
				t.Fatalf("EnsureLoaded: %v", err)
			}
			if hostInspected {
				t.Error("EnsureLoaded must decide from the microsandbox cache alone, never inspecting host Docker")
			}
			if len(msbClient.LoadedImages) != tc.wantLoadCount {
				t.Errorf(
					"load count = %d, want %d (%v)",
					len(msbClient.LoadedImages), tc.wantLoadCount, msbClient.LoadedImages,
				)
			}
		})
	}
}

func TestEnsureLoadedReloadsStaleContentUnderSameRef(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(_ context.Context, refs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			if len(refs) != 1 || refs[0] != rTag {
				t.Errorf("ImageSave refs = %v, want %q", refs, rTag)
			}
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	removeCalled := false
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:msb-old"}, nil
		},
		ImageRemoveFn: func(_ context.Context, _ string, _ bool) error {
			removeCalled = true
			return errors.New("database error: FOREIGN KEY constraint failed")
		},
	}
	ui := &termiotest.Mock{}
	info := ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:docker-new", DockerfileID: "abc"}
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient, info, ui); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if removeCalled {
		t.Error("expected the stale cached image to be refreshed without removal")
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != rTag {
		t.Errorf("LoadedImages = %v, want a reload of %q", msbClient.LoadedImages, rTag)
	}
}

func TestEnsureLoadedSkipsWhenCachedContentMatches(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	removed := false
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:same"}, nil
		},
		ImageRemoveFn: func(_ context.Context, _ string, _ bool) error { removed = true; return nil },
	}
	info := ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:same"}
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient, info, &termiotest.Mock{}); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if removed {
		t.Error("expected the matching cached image not to be removed")
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no reload when content matches, got %v", msbClient.LoadedImages)
	}
}

// TestEnsureLoadedReloadsLegacyImageWithoutDigest pins the cost of treating a
// missing digest as unknown content: a legacy cached image whose dockerfile-id
// label matches is still reloaded, so the fallback can never let a label match
// stand in for a content comparison. The load records a digest, so the reload
// happens at most once.
func TestEnsureLoadedReloadsLegacyImageWithoutDigest(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	labels := map[string]string{dockerfileIDLabelKey: "abc123"}
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "", Labels: labels}, nil
		},
	}
	info := ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc123"}
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient, info, &termiotest.Mock{}); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Errorf("expected a reload when the cached image records no digest, got %v", msbClient.LoadedImages)
	}
}

func TestEnsureLoadedLoadsWhenNotCached(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(_ context.Context, refs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			if len(refs) != 1 || refs[0] != "agents-sandbox/runner-test-project:abc" {
				t.Errorf("ImageSave refs = %v, want runner tag %q", refs, "agents-sandbox/runner-test-project:abc")
			}
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})

	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	if _, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{
			DockerTag: "agents-sandbox/runner-test-project:abc",
			Ref:       "agents-sandbox/runner-test-project:abc",
			Digest:    "sha256:new",
		},
		&termiotest.Mock{},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Fatalf("expected 1 image load, got %d", len(msbClient.LoadedImages))
	}
	if msbClient.LoadedImages[0] != "agents-sandbox/runner-test-project:abc" {
		t.Errorf(
			"loaded image ref = %q, want %q",
			msbClient.LoadedImages[0],
			"agents-sandbox/runner-test-project:abc",
		)
	}
}

// TestEnsureLoadedReusesContentRef pins the primary reuse path: when the cache
// already holds the content under the content reference, nothing is reimported.
func TestEnsureLoadedReusesContentRef(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	dockertest.WithNoopDockerMock(t)
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == contentRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termiotest.Mock{},
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
	configpathstest.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	nameRef := naming.RunnerImageRef("test-project", "opencode")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	dockertest.WithNoopDockerMock(t)
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == nameRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termiotest.Mock{},
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
	configpathstest.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	nameRef := naming.RunnerImageRef("test-project", "opencode")
	dockertest.WithNoopDockerMock(t)
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == nameRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termiotest.Mock{},
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
	configpathstest.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	dockertest.WithNoopDockerMock(t)
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == dockerTag {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: dockerTag, Ref: contentRef, Identity: testContentIdentity(), Digest: "sha256:same"},
		&termiotest.Mock{},
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
	configpathstest.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	var savedRefs []string
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(_ context.Context, refs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			savedRefs = refs
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	ui := &termiotest.Mock{}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
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
	configpathstest.WithMockConfigPaths(t)
	contentRef := naming.ContentImageRef("test-project", "opencode", "9f2c1a7b4d6e0f31")
	dockerTag := naming.DockerImageRef("test-project", "opencode")
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == dockerTag {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:old"}, nil
		},
	}
	ui := &termiotest.Mock{}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
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
	configpathstest.WithMockConfigPaths(t)
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: "", Digest: "sha256:new"},
		&termiotest.Mock{},
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
	configpathstest.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	dockertest.WithNoopDockerMock(t)
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:same"},
		&termiotest.Mock{},
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
	configpathstest.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:new"},
		&termiotest.Mock{},
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
	configpathstest.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.DockerImageRef("test-project", "opencode")
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msbtest.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:old"}, nil
		},
	}

	info, err := EnsureLoaded(context.Background(), docker.Get(), msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:new"},
		&termiotest.Mock{},
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

// TestCachedImageMatches pins the content-identity contract: only a cached
// digest equal to the Docker image ID counts as a match. A cached image that
// records no digest is unknown content and must reload — the dockerfile-id
// label is a source signature that can match while the bytes differ, so it is
// never trusted. An image the cache cannot inspect never matches either.
func TestCachedImageMatchesOnlyAnEqualDigest(t *testing.T) {
	cases := []struct {
		name      string
		msbDigest string
		msbLabels map[string]string
		msbNil    bool
		msbErr    error
		info      ImageInfo
		want      bool
	}{
		{
			name:      "equal digest matches",
			msbDigest: "sha256:same",
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:same"},
			want:      true,
		},
		{
			name:      "different digest never matches, even with equal label",
			msbDigest: "sha256:old",
			msbLabels: map[string]string{dockerfileIDLabelKey: "abc"},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			want:      false,
		},
		{
			name:      "missing digest never matches, even with equal label",
			msbLabels: map[string]string{dockerfileIDLabelKey: "abc123"},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc123"},
			want:      false,
		},
		{
			name:      "missing digest and no labels never matches",
			msbLabels: map[string]string{},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			want:      false,
		},
		{
			name:      "cached digest with no requested digest never matches",
			msbDigest: "sha256:same",
			info:      ImageInfo{DockerTag: "ref", Ref: "ref"},
			want:      false,
		},
		{
			name: "no cached digest and no requested digest never matches",
			info: ImageInfo{DockerTag: "ref", Ref: "ref"},
			want: false,
		},
		{
			name:   "failed inspect never matches",
			msbErr: errors.New("msb inspect boom"),
			info:   ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:x"},
			want:   false,
		},
		{
			name:   "nil inspect result never matches",
			msbNil: true,
			info:   ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msbClient := &msbtest.MockMsbClient{
				ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
					if tc.msbErr != nil {
						return nil, tc.msbErr
					}
					if tc.msbNil {
						return nil, nil //nolint:nilnil // the seam can return a nil config with no error
					}
					return &msb.ImageConfig{Digest: tc.msbDigest, Labels: tc.msbLabels}, nil
				},
			}
			if got := cachedImageMatches(context.Background(), msbClient, tc.info.Ref, tc.info); got != tc.want {
				t.Errorf("cachedImageMatches() = %v, want %v", got, tc.want)
			}
		})
	}
}

// testContentIdentity is the structured identity of the image the reuse tests
// describe: project "test-project", agent opencode, content id 9f2c1a7b4d6e0f31.
func testContentIdentity() naming.ImageIdentity {
	return naming.ImageIdentity{Slug: "test-project", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31"}
}

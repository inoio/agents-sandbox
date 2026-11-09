package image

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"

	buildkitclient "github.com/moby/buildkit/client"
	"github.com/tonistiigi/fsutil"
)

// TestBuildKitSolveOptFrontendAndExports pins the dockerfile.v0 frontend over
// the rendered Dockerfile and the moby exporter tagging the image, so the
// BuildKit path produces the same tagged image the classic path does.
func TestBuildKitSolveOptFrontendAndExports(t *testing.T) {
	const tag = "agents-sandbox/test:latest"
	opt, cleanup, err := buildKitSolveOpt(
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		tag,
		false,
	)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	if opt.Frontend != "dockerfile.v0" {
		t.Errorf("Frontend = %q, want dockerfile.v0", opt.Frontend)
	}
	if got := opt.FrontendAttrs["filename"]; got != "Dockerfile" {
		t.Errorf(`FrontendAttrs["filename"] = %q, want "Dockerfile"`, got)
	}
	wantExports := []buildkitclient.ExportEntry{{Type: "moby", Attrs: map[string]string{"name": tag}}}
	if !reflect.DeepEqual(opt.Exports, wantExports) {
		t.Errorf("Exports = %#v, want %#v", opt.Exports, wantExports)
	}
}

// TestBuildKitSolveOptLocalMountsPointAtTheContextDir pins that the daemon
// receives the one context directory under both local source names the
// dockerfile.v0 frontend reads, and that the directory serves the written
// context.
func TestBuildKitSolveOptLocalMountsPointAtTheContextDir(t *testing.T) {
	opt, cleanup, err := buildKitSolveOpt(
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		"tag",
		false,
	)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	contextFS, contextOK := opt.LocalMounts["context"]
	dockerfileFS, dockerfileOK := opt.LocalMounts["dockerfile"]
	if !contextOK || !dockerfileOK {
		t.Fatalf("LocalMounts = %v, want both context and dockerfile entries", opt.LocalMounts)
	}
	if contextFS != dockerfileFS {
		t.Error("LocalMounts context and dockerfile entries differ, want the same context directory")
	}
	names := buildKitFSNames(t, contextFS)
	if !slices.Contains(names, "Dockerfile") {
		t.Errorf("context FS serves %v, want it to include the Dockerfile", names)
	}
}

// TestBuildKitSolveOptNoCacheFollowsParameter pins that a no-cache build is
// expressed through the dockerfile frontend's no-cache attribute (empty when
// set, absent when the build may use the cache), since a SolveOpt carries no
// NoCache field.
func TestBuildKitSolveOptNoCacheFollowsParameter(t *testing.T) {
	for _, noCache := range []bool{true, false} {
		t.Run(strconv.FormatBool(noCache), func(t *testing.T) {
			opt, cleanup, err := buildKitSolveOpt(
				testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
				"tag",
				noCache,
			)
			if err != nil {
				t.Fatalf("buildKitSolveOpt: %v", err)
			}
			defer cleanup()

			value, present := opt.FrontendAttrs["no-cache"]
			if present != noCache {
				t.Errorf("FrontendAttrs[\"no-cache\"] present = %v, want %v", present, noCache)
			}
			if noCache && value != "" {
				t.Errorf(`FrontendAttrs["no-cache"] = %q, want ""`, value)
			}
		})
	}
}

// TestBuildKitSolveOptCacheEntriesEmptyWhenUnset pins the cache-neutral
// default: a build with no cache configuration carries no cache entries.
func TestBuildKitSolveOptCacheEntriesEmptyWhenUnset(t *testing.T) {
	opt, cleanup, err := buildKitSolveOpt(
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		"tag",
		false,
	)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	if len(opt.CacheImports) != 0 || len(opt.CacheExports) != 0 {
		t.Errorf("CacheImports = %v, CacheExports = %v, want both empty", opt.CacheImports, opt.CacheExports)
	}
}

// TestBuildKitSolveOptCacheEntriesPassThroughVerbatim pins that a configured
// cache spec's type and attributes reach the solve options uninterpreted.
func TestBuildKitSolveOptCacheEntriesPassThroughVerbatim(t *testing.T) {
	req := testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{})
	req.cacheFrom = []string{"type=gha,scope=runner-image,mode=max"}
	req.cacheTo = []string{"type=registry,ref=example.com/cache:main"}
	opt, cleanup, err := buildKitSolveOpt(req, "tag", false)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	wantImports := []buildkitclient.CacheOptionsEntry{
		{Type: "gha", Attrs: map[string]string{"scope": "runner-image", "mode": "max"}},
	}
	if !reflect.DeepEqual(opt.CacheImports, wantImports) {
		t.Errorf("CacheImports = %#v, want %#v", opt.CacheImports, wantImports)
	}
	wantExports := []buildkitclient.CacheOptionsEntry{
		{Type: "registry", Attrs: map[string]string{"ref": "example.com/cache:main"}},
	}
	if !reflect.DeepEqual(opt.CacheExports, wantExports) {
		t.Errorf("CacheExports = %#v, want %#v", opt.CacheExports, wantExports)
	}
}

// TestBuildKitSolveOptCleanupRemovesTheContextDir pins the returned cleanup's
// contract: once called, the temporary directory the local mounts served no
// longer exists.
func TestBuildKitSolveOptCleanupRemovesTheContextDir(t *testing.T) {
	opt, cleanup, err := buildKitSolveOpt(
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		"tag",
		false,
	)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}

	contextFS, ok := opt.LocalMounts["context"]
	if !ok {
		t.Fatal("LocalMounts has no context entry")
	}
	if len(buildKitFSNames(t, contextFS)) == 0 {
		t.Fatal("context FS served no entries before cleanup")
	}
	cleanup()
	if got := len(buildKitFSNames(t, contextFS)); got != 0 {
		t.Errorf("context FS still serves %d entries after cleanup, want the directory removed", got)
	}
}

// TestBuildKitWriteContextWritesDockerfileAndContextFiles pins that the
// on-disk BuildKit context carries the rendered Dockerfile and every
// buildContextFiles() entry at the same mode the classic context tar gives it,
// so the two builders receive byte-identical sources.
func TestBuildKitWriteContextWritesDockerfileAndContextFiles(t *testing.T) {
	dir := t.TempDir()
	dockerfile := []byte("FROM scratch\nRUN echo hi\n")
	if err := writeBuildKitContext(dir, dockerfile); err != nil {
		t.Fatalf("writeBuildKitContext: %v", err)
	}

	gotDockerfile, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !bytes.Equal(gotDockerfile, dockerfile) {
		t.Errorf("Dockerfile = %q, want the rendered %q", gotDockerfile, dockerfile)
	}
	for _, file := range buildContextFiles() {
		path := filepath.Join(dir, file.name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read context file %s: %v", file.name, err)
		}
		if !bytes.Equal(content, file.content) {
			t.Errorf("context file %s carries %d bytes, want %d", file.name, len(content), len(file.content))
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat context file %s: %v", file.name, err)
		}
		if got := info.Mode().Perm(); got != os.FileMode(file.mode) {
			t.Errorf("context file %s mode = %o, want %o", file.name, got, os.FileMode(file.mode))
		}
	}
}

// buildKitFSNames walks an fsutil.FS and returns the entry names it serves; a
// removed root serves nothing, which is how the cleanup test observes deletion.
func buildKitFSNames(t *testing.T, f fsutil.FS) []string {
	t.Helper()
	var names []string
	err := f.Walk(context.Background(), ".", func(_ string, entry fs.DirEntry, _ error) error {
		if entry != nil {
			names = append(names, entry.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fsutil.FS: %v", err)
	}
	return names
}

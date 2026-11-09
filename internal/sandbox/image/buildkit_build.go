package image

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	buildkitclient "github.com/moby/buildkit/client"
	"github.com/tonistiigi/fsutil"
)

// buildKitSolveOpt assembles the solve options for one BuildKit build: the
// rendered Dockerfile and its context files are written to a fresh temporary
// directory and served to the daemon under both local source names the
// dockerfile.v0 frontend reads ("context" and "dockerfile"), the moby exporter
// tags the produced image, noCache bypasses the build cache through the
// frontend's no-cache attribute, and the configured cache specs become the
// solve's cache import/export entries. The returned cleanup removes the
// temporary directory.
func buildKitSolveOpt(req buildRequest, tag string, noCache bool) (buildkitclient.SolveOpt, func(), error) {
	dir, err := os.MkdirTemp("", "agents-sandbox-buildkit-*")
	if err != nil {
		return buildkitclient.SolveOpt{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err = writeBuildKitContext(dir, req.plan.rendered); err != nil {
		cleanup()
		return buildkitclient.SolveOpt{}, nil, err
	}
	contextFS, err := fsutil.NewFS(dir)
	if err != nil {
		cleanup()
		return buildkitclient.SolveOpt{}, nil, err
	}
	frontendAttrs := map[string]string{"filename": "Dockerfile"}
	if noCache {
		frontendAttrs["no-cache"] = ""
	}
	//nolint:exhaustruct_v5 // the solve carries only the fields this builder controls; the rest stay zero
	return buildkitclient.SolveOpt{
		Frontend:      "dockerfile.v0",
		FrontendAttrs: frontendAttrs,
		LocalMounts: map[string]fsutil.FS{
			"context":    contextFS,
			"dockerfile": contextFS,
		},
		//nolint:exhaustruct_v5 // the moby exporter needs only Type and Attrs
		Exports: []buildkitclient.ExportEntry{
			{Type: "moby", Attrs: map[string]string{"name": tag}},
		},
		CacheImports: buildKitCacheEntries(req.cacheFrom),
		CacheExports: buildKitCacheEntries(req.cacheTo),
	}, cleanup, nil
}

// writeBuildKitContext writes the synthetic build context — the rendered
// Dockerfile followed by the build-context files — into dir as regular files
// with the same modes the classic path gives the tar entries, so the BuildKit
// filesync and the classic tar serve byte-identical sources.
func writeBuildKitContext(dir string, dockerfile []byte) error {
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfile, dockerfileMode); err != nil {
		return fmt.Errorf("write build context Dockerfile: %w", err)
	}
	for _, file := range buildContextFiles() {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(
			path,
			file.content,
			//nolint:gosec // the context modes are constants (0644/0755)
			os.FileMode(file.mode),
		); err != nil {
			return fmt.Errorf("write build context %s: %w", file.name, err)
		}
	}
	return nil
}

// buildKitCacheEntries parses the configured cache specs into the cache
// import/export entries a solve carries; an empty input yields no entries.
func buildKitCacheEntries(specs []string) []buildkitclient.CacheOptionsEntry {
	entries := make([]buildkitclient.CacheOptionsEntry, 0, len(specs))
	for _, spec := range specs {
		entries = append(entries, parseBuildKitCacheSpec(spec))
	}
	return entries
}

// parseBuildKitCacheSpec parses one BuildKit cache spec of the form
// "type=gha,scope=x,mode=max" into the entry a SolveOpt carries. Only the
// shape is parsed — the type and attribute values pass through to BuildKit
// uninterpreted, so an invalid backend or attribute is the daemon's error to
// report.
func parseBuildKitCacheSpec(spec string) buildkitclient.CacheOptionsEntry {
	attrs := map[string]string{}
	entryType := ""
	for i, part := range strings.Split(spec, ",") {
		key, value, hasValue := strings.Cut(part, "=")
		if !hasValue {
			continue
		}
		if i == 0 && key == "type" {
			entryType = value
			continue
		}
		attrs[key] = value
	}
	return buildkitclient.CacheOptionsEntry{Type: entryType, Attrs: attrs}
}

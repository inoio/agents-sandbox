// Package reprovision provides sandbox reprovisioning capabilities, including
// config file loading/provisioning, environment and secret management, and
// VM reconfiguration planning and resolution.
package reprovision

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

// defaultSandboxUser is the runtime user inside the project VM that opencode and
// startup hooks run as. Provisioned files are chowned to this user because the
// microsandbox SDK's file writes create files owned by root.
const defaultSandboxUser = "dev"

// Provision writes the merged opencode config (when snippets exist), each home
// file, and the drop-in copy into the sandbox, creates parent directories as
// needed, removes the marked stale paths, then chowns every written path and
// created directory to the runtime user so the files are readable by opencode
// and startup hooks (the SDK's file writes create root-owned files and
// directories).
func Provision(ctx context.Context, sb msb.Sandbox, cf *ConfigFiles) (retErr error) {
	fs := sb.FS()
	paths := make([]string, 0)
	// Chown runs as a finalizer (try/finally): it executes whether the writes
	// succeed or fail, so partially-provisioned root-owned paths are still
	// reclaimed. Both the primary error and a chown error are joined so neither
	// is lost.
	defer func() {
		if len(paths) == 0 {
			return
		}
		if err := chownPaths(ctx, sb, paths); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	// Remove stale config first so it cannot shadow the files written below.
	// Best-effort: the merged config is written to the last-loaded filename
	// (opencode.jsonc), so a failed removal is non-fatal.
	removeStalePaths(ctx, fs, cf.Remove)
	if cf.HasSnippets && len(cf.OpenCode) > 0 {
		ocPath := cf.MergedPath
		made, err := mkdirAllFS(ctx, fs, filepath.Dir(ocPath))
		if err != nil {
			return err
		}
		paths = append(paths, made...)
		if err := fs.Write(ctx, ocPath, cf.OpenCode); err != nil {
			return fmt.Errorf("write opencode config: %w", err)
		}
		paths = append(paths, ocPath)
	}
	for path, data := range cf.HomeFiles {
		made, err := mkdirAllFS(ctx, fs, filepath.Dir(path))
		if err != nil {
			return err
		}
		paths = append(paths, made...)
		if err := fs.Write(ctx, path, data); err != nil {
			return fmt.Errorf("write home file %s: %w", path, err)
		}
		paths = append(paths, path)
	}
	for path, data := range cf.Provisioned {
		made, err := mkdirAllFS(ctx, fs, filepath.Dir(path))
		if err != nil {
			return err
		}
		paths = append(paths, made...)
		if err := fs.Write(ctx, path, data); err != nil {
			return fmt.Errorf("write provisioned file %s: %w", path, err)
		}
		paths = append(paths, path)
	}
	return nil
}

// removeStalePaths deletes the marked stale paths so a previous provisioning
// run's host config cannot shadow the files written by Provision. Failures are
// ignored: the merged config is written to the last-loaded filename
// (opencode.jsonc), so a failed removal is non-fatal.
func removeStalePaths(ctx context.Context, fs msb.SandboxFS, remove []string) {
	for _, p := range remove {
		_ = fs.Remove(ctx, p)
	}
}

// chownPaths runs a single chown -R over the given paths so all provisioned
// files and created directories are owned by the runtime user.
func chownPaths(ctx context.Context, sb msb.Sandbox, paths []string) error {
	// Deduplicate: mkdirAllFS may create the same ancestor for several writes.
	unique := make([]string, 0, len(paths))
	sort.Strings(paths)
	var last string
	for _, p := range paths {
		if p != last {
			unique = append(unique, p)
			last = p
		}
	}
	cmd := "chown -R " + defaultSandboxUser + ":" + defaultSandboxUser + " " + strings.Join(unique, " ")
	if out, err := sb.Shell(ctx, cmd, msbSdk.WithExecUser("root")); err != nil {
		return fmt.Errorf("chown provisioned files: %w", err)
	} else if !out.Success() {
		return fmt.Errorf("chown provisioned files: %s", strings.TrimSpace(out.Stderr()))
	}
	return nil
}

// mkdirAllFS creates path and all missing parents, tolerating existing dirs.
func mkdirAllFS(ctx context.Context, fs msb.SandboxFS, path string) ([]string, error) {
	if path == "" || path == "/" || path == "." {
		return nil, nil
	}
	// Walk up to an existing ancestor, then mkdir each missing segment.
	if ok, _ := fs.Exists(ctx, path); ok {
		return nil, nil
	}
	made, prevErr := mkdirAllFS(ctx, fs, filepath.Dir(path))
	if prevErr != nil {
		return nil, prevErr
	}
	err := fs.Mkdir(ctx, path)
	if err == nil {
		made = append(made, path)
	}
	return made, prevErr
}

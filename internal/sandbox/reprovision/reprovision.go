// Package reprovision provides sandbox reprovisioning capabilities, including
// config file loading/provisioning, environment and secret management, and
// VM reconfiguration planning and resolution.
package reprovision

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

// Provision writes the merged opencode config (when snippets exist) and each
// home file into the sandbox, creating parent directories as needed.
func Provision(ctx context.Context, fs msb.SandboxFS, cf *ConfigFiles) error {
	if cf.HasSnippets && len(cf.OpenCode) > 0 {
		ocPath := OpenCodeConfigPath(VMHomeDir)
		if err := mkdirAllFS(ctx, fs, filepath.Dir(ocPath)); err != nil {
			return err
		}
		if err := fs.Write(ctx, ocPath, cf.OpenCode); err != nil {
			return fmt.Errorf("write opencode config: %w", err)
		}
	}
	for path, data := range cf.HomeFiles {
		if err := mkdirAllFS(ctx, fs, filepath.Dir(path)); err != nil {
			return err
		}
		if err := fs.Write(ctx, path, data); err != nil {
			return fmt.Errorf("write home file %s: %w", path, err)
		}
	}
	return nil
}

// mkdirAllFS creates path and all missing parents, tolerating existing dirs.
func mkdirAllFS(ctx context.Context, fs msb.SandboxFS, path string) error {
	if path == "" || path == "/" || path == "." {
		return nil
	}
	// Walk up to an existing ancestor, then mkdir each missing segment.
	if ok, _ := fs.Exists(ctx, path); ok {
		return nil
	}
	if err := mkdirAllFS(ctx, fs, filepath.Dir(path)); err != nil {
		return err
	}
	return fs.Mkdir(ctx, path)
}

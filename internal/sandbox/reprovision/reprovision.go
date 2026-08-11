// Package reprovision provides sandbox reprovisioning capabilities, including
// config file loading/provisioning, environment and secret management, and
// VM reconfiguration planning and resolution.
package reprovision

import (
	"context"
	"fmt"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
)

// ProvisionSandbox writes the merged opencode configuration files into the
// sandbox's /home/dev/.config/opencode directory.
func ProvisionSandbox(ctx context.Context, fs msb.SandboxFS, configFiles map[string][]byte) error {
	if err := fs.Mkdir(ctx, "/home/dev/.config/opencode"); err != nil {
		return fmt.Errorf("mkdir opencode config: %w", err)
	}
	for fname, data := range configFiles {
		if err := fs.Write(ctx, "/home/dev/.config/opencode/"+fname, data); err != nil {
			return fmt.Errorf("write config file %s: %w", fname, err)
		}
	}
	return nil
}

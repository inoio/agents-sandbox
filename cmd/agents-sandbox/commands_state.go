package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// buildStateCmd groups the launcher-state maintenance commands under `state`.
func buildStateCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdState,
		Args:  cobra.NoArgs,
		Short: "Maintain the launcher state",
	}
	cmd.AddCommand(buildStateRepairCmd(ui))
	return cmd
}

// buildStateRepairCmd builds `state repair`, the explicit recovery path for a
// corrupt launcher-global state file (#332): it moves the file aside instead of
// deleting it, so the next run starts from a fresh launcher state.
func buildStateRepairCmd(ui termio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdRepair,
		Args:  cobra.NoArgs,
		Short: "Quarantine a corrupt launcher-global state file",
		Long: "Quarantine a corrupt launcher-global state file.\n\n" +
			"An unparsable state.yaml at the top of the state directory blocks update checks, the runtime\n" +
			"migration, and identity adoption. This command renames it to state.yaml.corrupt-<timestamp>\n" +
			"so the next run starts from a fresh launcher state. A healthy or missing file is left alone.",
		RunE: func(_ *cobra.Command, _ []string) error {
			quarantinedPath, err := state.RepairRootState()
			if err != nil {
				return fmt.Errorf("repair launcher state: %w", err)
			}
			if quarantinedPath == "" {
				ui.Out("Launcher state is healthy; nothing to repair.")
				return nil
			}
			ui.Outf("Moved the corrupt launcher state to %s.", quarantinedPath)
			ui.Out("Update checks, migration records, and adoption claims start fresh on the next run.")
			return nil
		},
	}
}

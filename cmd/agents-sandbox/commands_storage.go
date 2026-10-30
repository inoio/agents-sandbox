package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/cliout"
	"github.com/inoio/agents-sandbox/internal/sandbox/storage"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// buildDFCmd builds `df [--verbose] [--format json]`, which reports the private
// runtime home's storage usage by category. --verbose adds per-object rows and
// host paths.
func buildDFCmd(ui termio.UI) *cobra.Command {
	var (
		verbose bool
		format  string
	)
	cmd := &cobra.Command{
		Use:   cmdDF,
		Args:  cobra.NoArgs,
		Short: "Show storage usage of the private runtime home",
		Annotations: map[string]string{
			annotationAlsoAs: cmdStorage + " " + cmdDF,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := cliout.ResolveMode(format, ui.Quiet())
			if err != nil {
				return err
			}
			includeObjects := verbose || mode == cliout.ModeJSON
			report, err := storage.Observe(cmd.Context(), storage.Options{IncludeObjects: includeObjects})
			if err != nil {
				return fmt.Errorf("observe storage: %w", err)
			}
			switch mode {
			case cliout.ModeJSON:
				return cliout.WriteJSON(ui, newStorageReportJSON(report))
			case cliout.ModeIdentifiers:
				for _, category := range report.Categories {
					ui.Print(storageDisplayName(category.Name))
				}
			case cliout.ModeTable:
				printStorageReport(ui, report, verbose)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verbose, flagVerbose, false, "List per-object rows and host paths")
	cmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	return cmd
}

// buildStorageCmd groups the storage report and prune commands under `storage`,
// aliasing the top-level `df` and `prune`.
func buildStorageCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdStorage,
		Args:  cobra.NoArgs,
		Short: "Inspect and reclaim runtime storage",
	}
	cmd.AddCommand(buildDFCmd(ui))
	cmd.AddCommand(buildPruneCmd(ui))
	return cmd
}

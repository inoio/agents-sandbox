package main

import (
	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
)

// cmdBuilderRegistry is the hidden daemon entry point that cacheregistry.Ensure
// re-execs. It is never shown to users.
const cmdBuilderRegistry = cacheregistry.CommandName

func buildBuilderRegistryCmd() *cobra.Command {
	return &cobra.Command{
		Use:                cmdBuilderRegistry,
		Hidden:             true,
		DisableFlagParsing: true,
		PersistentPreRunE:  func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			return cacheregistry.ServeCommandArgs(cmd.Context(), args)
		},
	}
}

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
)

// cmdBuilderRegistry is the hidden daemon entry point that cacheregistry.Ensure
// re-execs. It is never shown to users.
const cmdBuilderRegistry = cacheregistry.CommandName

const flagBuilderRegistryConfig = "config"

func buildBuilderRegistryCmd() *cobra.Command {
	return &cobra.Command{
		Use:                cmdBuilderRegistry,
		Hidden:             true,
		DisableFlagParsing: true,
		PersistentPreRunE:  func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, err := registryConfigPathFromArgs(args)
			if err != nil {
				return err
			}
			return cacheregistry.Serve(cmd.Context(), configPath)
		},
	}
}

func registryConfigPathFromArgs(args []string) (string, error) {
	flag := "--" + flagBuilderRegistryConfig
	for index, arg := range args {
		if arg != flag {
			continue
		}
		if index+1 >= len(args) {
			return "", fmt.Errorf("%s requires a config path", flag)
		}
		return args[index+1], nil
	}
	return "", fmt.Errorf("missing %s", flag)
}

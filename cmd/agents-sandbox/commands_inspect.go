package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/cliout"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// buildInspectCmd builds an `<group> inspect <name>` command. It resolves the
// shared output mode, inspects the object by its argument, and renders the
// detail through the shared printInspect path (identifier / JSON / human block).
func buildInspectCmd[T any](
	ui termio.UI,
	argName, short string,
	inspect func(ctx context.Context, name string) (T, error),
	identifier func(T) string,
	human func(termio.UI, T),
	jsonValue func(T) any,
) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   cmdInspect + " <" + argName + ">",
		Args:  cobra.ExactArgs(1),
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := cliout.ResolveMode(format, ui.Quiet())
			if err != nil {
				return err
			}
			detail, err := inspect(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printInspect(ui, mode, identifier(detail),
				func() { human(ui, detail) }, jsonValue(detail))
		},
	}
	cmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	return cmd
}

// buildVolumeInspectCmd builds `volume inspect <name>`, which prints the full
// detail of a home volume.
func buildVolumeInspectCmd(ui termio.UI) *cobra.Command {
	return buildInspectCmd(ui, "name", "Show detailed information about a home volume",
		volume.InspectVolume,
		func(detail volume.VolumeDetail) string { return detail.Name },
		printVolumeInspect,
		func(detail volume.VolumeDetail) any { return newVolumeInspectJSON(detail) })
}

// buildImageInspectCmd builds `image inspect <ref>`, which prints a cached
// runner image's manifest, layers, and OCI config.
func buildImageInspectCmd(ui termio.UI) *cobra.Command {
	return buildInspectCmd(ui, "ref", "Show detailed information about a cached runner image",
		image.InspectImage,
		func(detail image.ImageDetail) string { return detail.Reference },
		printImageInspect,
		func(detail image.ImageDetail) any { return newImageInspectJSON(detail) })
}

// buildSandboxInspectCmd builds `sandbox inspect <name>`, which prints a
// sandbox VM's status, image, labels, mounts, resources, ID, and backend.
func buildSandboxInspectCmd(ui termio.UI) *cobra.Command {
	return buildInspectCmd(ui, "name", "Show detailed information about a sandbox VM",
		sandbox.InspectSandbox,
		func(detail sandbox.SandboxDetail) string { return detail.Name },
		printSandboxInspect,
		func(detail sandbox.SandboxDetail) any { return newSandboxInspectJSON(detail) })
}

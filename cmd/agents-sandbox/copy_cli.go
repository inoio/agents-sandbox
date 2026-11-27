package main

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/cliout"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// copyVMOperandPrefix marks the guest side of a sandbox copy operand.
const copyVMOperandPrefix = "vm:"

// copyJSON is the --format json shape of a sandbox copy.
type copyJSON struct {
	VM          string `json:"vm"`
	Direction   string `json:"direction"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// buildCopyCmd builds `sandbox copy <src> <dst>`, which transfers one file
// between the host and the current checkout's running project VM.
func buildCopyCmd(ui termio.UI) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:     cmdCopy + " <src> <dst>",
		Aliases: cmdCopyAliases,
		Args:    cobra.ExactArgs(2),
		Short:   "Copy a file between the host and the project VM",
		Long: "Copy a single file between the host and the current checkout's project VM, either " +
			"direction. Exactly one operand is a host path and the other a vm:<guest-path> (an absolute " +
			"path inside the VM). The VM must be running; cp never starts a stopped VM.",
		Annotations: map[string]string{
			annotationAlsoAs: cmdSandbox + " " + cmdCopy,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := cliout.ResolveMode(format, ui.Quiet())
			if err != nil {
				return err
			}
			direction, hostPath, guestPath, err := parseCopyOperands(args[0], args[1])
			if err != nil {
				return err
			}
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			result, err := sandbox.CopyProjectFile(cmd.Context(), configpaths.Get(), msb.Get(),
				direction, hostPath, guestPath, ui, a.Name())
			if err != nil {
				return err
			}
			return renderCopy(ui, mode, result, args[0], args[1])
		},
	}
	cmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	bindAgentFlag(cmd)
	return cmd
}

// parseCopyOperands splits the two operands into a direction and the host and
// guest paths. Exactly one operand carries the vm: prefix, and the stripped
// guest path must be absolute.
func parseCopyOperands(source, destination string) (sandbox.CopyDirection, string, string, error) {
	sourceIsVM := strings.HasPrefix(source, copyVMOperandPrefix)
	destinationIsVM := strings.HasPrefix(destination, copyVMOperandPrefix)
	switch {
	case sourceIsVM && destinationIsVM:
		return 0, "", "", errors.New(
			"exactly one of <src> and <dst> may be a vm: path; VM-to-VM copies are not supported",
		)
	case !sourceIsVM && !destinationIsVM:
		return 0, "", "", errors.New(
			"exactly one of <src> and <dst> must be a vm: path; prefix the guest file with a vm: prefix",
		)
	case sourceIsVM:
		guestPath := strings.TrimPrefix(source, copyVMOperandPrefix)
		if !path.IsAbs(guestPath) {
			return 0, "", "", fmt.Errorf("the vm: guest path %q must be absolute", guestPath)
		}
		return sandbox.CopyVMToHost, destination, guestPath, nil
	default:
		guestPath := strings.TrimPrefix(destination, copyVMOperandPrefix)
		if !path.IsAbs(guestPath) {
			return 0, "", "", fmt.Errorf("the vm: guest path %q must be absolute", guestPath)
		}
		return sandbox.CopyHostToVM, source, guestPath, nil
	}
}

// renderCopy prints the copy result: one line by default, a JSON document under
// --format json, and nothing under -q (a copy has no identifier to print).
func renderCopy(ui termio.UI, mode cliout.Mode, result sandbox.CopyResult, source, destination string) error {
	switch mode {
	case cliout.ModeJSON:
		return cliout.WriteJSON(ui, copyJSON{
			VM:          result.VMName,
			Direction:   result.Direction.String(),
			Source:      source,
			Destination: destination,
		})
	case cliout.ModeIdentifiers:
		// A copy has no identifier to print.
	case cliout.ModeTable:
		ui.Infof("Copied %s -> %s", source, destination)
	}
	return nil
}

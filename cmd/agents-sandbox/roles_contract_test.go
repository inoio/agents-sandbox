package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// assertAssignable fails to compile unless its argument's type is assignable to
// the explicitly given type parameter.
func assertAssignable[T any](_ T) {}

// TestCommandBuildersUseNarrowRoles pins each command builder to the narrowest
// role it actually uses: it fails to compile if a builder widens back to
// termio.UI.
func TestCommandBuildersUseNarrowRoles(_ *testing.T) {
	assertAssignable[func(*cobra.Command, termio.Logger) *cobra.Command](buildTreeCmd)
	assertAssignable[func(termio.Logger) *cobra.Command](buildUpgradeCmd)
	assertAssignable[func(termio.Prompter) *cobra.Command](buildImageRemoveCmd)
	assertAssignable[func(termio.Prompter) *cobra.Command](buildVolumeRemoveCmd)
	assertAssignable[func(purgeUI) *cobra.Command](buildSandboxRemoveCmd)
}

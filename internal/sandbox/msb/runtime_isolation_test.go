//go:build !msb

package msb

// installIsolatedRuntimeHome is a no-op outside the msb-tagged build. The
// untagged tests never load the native SDK, so they must not create a runtime
// home or touch the environment, and compile-all's -run=^$ must stay inert.
func installIsolatedRuntimeHome() func() { return func() {} }

// sweepIsolatedSandboxes is a no-op outside the msb-tagged build: the untagged
// tests never create sandboxes, so there is nothing to sweep and the native
// SDK must not be touched.
func sweepIsolatedSandboxes() {}

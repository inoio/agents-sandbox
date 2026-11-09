//go:build !msb

//deadcode:ignore reason="test support: isolated-runtime helpers, no-ops outside the msb-tagged build"
package runtime

// InstallIsolatedRuntimeHome is a no-op outside the msb-tagged build. The
// untagged tests never load the native SDK, so they must not create a runtime
// home or touch the environment, and compile-all's -run=^$ must stay inert.
func InstallIsolatedRuntimeHome() func() { return func() {} }

// SweepIsolatedSandboxes is a no-op outside the msb-tagged build: the untagged
// tests never create sandboxes, so there is nothing to sweep and the native
// SDK must not be touched.
func SweepIsolatedSandboxes() error { return nil }

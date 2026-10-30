//go:build !msb

package msb

// installIsolatedRuntimeHome is a no-op outside the msb-tagged build. The
// untagged tests never load the native SDK, so they must not create a runtime
// home or touch the environment, and compile-all's -run=^$ must stay inert.
func installIsolatedRuntimeHome() func() { return func() {} }

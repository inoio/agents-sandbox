package options

import "time"

const (
	// SandboxStopTimeout is the default timeout for stopping a sandbox.
	SandboxStopTimeout = 30 * time.Second
	// DefaultVMIdleTimeout is the default idle timeout before a VM stops.
	DefaultVMIdleTimeout = 30 * time.Second
)

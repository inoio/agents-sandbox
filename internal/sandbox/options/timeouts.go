package options

import "time"

const (
	// SandboxStopTimeout is the default timeout for stopping a sandbox.
	SandboxStopTimeout = 30 * time.Second
	// DefaultVMIdleTimeout is the default idle timeout before a VM stops,
	// applied when RunOptions.IdleTimeout is unset. It is the single source of
	// truth for the default: the config layer carries the user's
	// auto-stop-timeout value as-is and never substitutes its own default.
	DefaultVMIdleTimeout = 10 * time.Second
)

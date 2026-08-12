package sandbox

import "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"

// Re-exported state module functions. These preserve the public API of the
// sandbox core so that tests within internal/sandbox continue to compile after
// the state module was extracted.

//nolint:gochecknoglobals // Re-exports preserve the sandbox core public API.
var (
	SetStateDirForTest = state.SetStateDirForTest
	WriteState         = state.WriteState
)

// HomeState is a re-export of state.HomeState for the sandbox core public API.
type (
	// HomeState tracks home volume, image digest, and env/secret fingerprints.
	HomeState = state.HomeState
)

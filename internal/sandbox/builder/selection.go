package builder

import "github.com/inoio/agents-sandbox/internal/viperconfig"

const (
	backendMsb  = "msb"
	backendMoby = "moby"
)

// resolveBackendName returns the selected backend name, defaulting to msb for
// any unset or unknown value.
func resolveBackendName() string {
	if viperconfig.BuilderBackend() == backendMoby {
		return backendMoby
	}
	return backendMsb
}

// SelectedName returns the selected builder backend name, defaulting to msb for
// any unset or unknown value. It is the selection seam other packages gate
// backend-specific prerequisites on.
func SelectedName() string {
	return resolveBackendName()
}

// Get returns the builder backend selected by launcher config and the
// AGENTS_SANDBOX_BUILDER environment override.
var Get = func() Builder {
	if resolveBackendName() == backendMoby {
		return newMobyBuilder()
	}
	return newClient()
}

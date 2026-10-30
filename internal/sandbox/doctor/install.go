package doctor

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

var ensureInstalledFunc = func(ctx context.Context) error {
	return msb.Get().EnsureInstalled(ctx)
}

// ensureMsbInstalled runs the msb runtime setup.
func ensureMsbInstalled(ctx context.Context) error {
	return ensureInstalledFunc(ctx)
}

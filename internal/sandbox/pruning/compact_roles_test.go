package pruning

import (
	"context"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// assertAssignable fails to compile unless its argument's type is assignable to
// the explicitly given type parameter.
func assertAssignable[T any](_ T) {}

// TestCompactSandboxesAcceptsLogger pins CompactSandboxes to the narrow Logger
// role it uses; it fails to compile if the parameter widens to termio.UI.
func TestCompactSandboxesAcceptsLogger(_ *testing.T) {
	assertAssignable[func(context.Context, string, msbSdk.DiskCompactionOptions, termio.Logger) (CompactReport, error)](
		CompactSandboxes,
	)
}

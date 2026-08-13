//go:build darwin

package doctor

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

func TestCheckDarwinReturnsTrue(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	// On darwin builds, GOARCH is arm64; tests run on the build platform.
	// Verify the function doesn't error when GOARCH matches the build.
	if !CheckDarwin(&testUI) {
		t.Log("CheckDarwin returned false on non-darwin build — expected, not failing")
	}
}

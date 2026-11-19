package scripts

import (
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil/fakecmd"
	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	fakecmd.Register("gh", fakeGhHandler)
	if status := fakecmd.Dispatch(); status >= 0 {
		os.Exit(status)
	}
	mocks.InitFailFastMocks(m)
}

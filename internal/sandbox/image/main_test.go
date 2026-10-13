package image

import (
	"os"
	"testing"

	termio "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	termio.InstallFailFastMocks()
	os.Exit(m.Run())
}

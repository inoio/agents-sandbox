package reprovision

import (
	"testing"

	termio "gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	termio.InitFailFastMocks(m)
}

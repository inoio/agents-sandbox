package naming

import (
	"testing"

	termio "github.com/inoio/opencode-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	termio.InitFailFastMocks(m)
}

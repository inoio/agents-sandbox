package testutil

import (
	"testing"

	termio "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	termio.InitFailFastMocks(m)
}

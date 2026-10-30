package session

import (
	"testing"
	"time"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	// A mock whose sessions never quiesce must fail fast, not poll out the
	// production interval; a test that needs the real interval overrides it.
	waitQuiescentPollInterval = 10 * time.Millisecond
	mocks.InitFailFastMocks(m)
}

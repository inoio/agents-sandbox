package main

import (
	"testing"

	mocks "gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}

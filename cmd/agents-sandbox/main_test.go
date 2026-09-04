package main

import (
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}

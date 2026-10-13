package main

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	mocks.InstallFailFastMocks()
	doctor.InstallFailFast()
	m.Run()
}

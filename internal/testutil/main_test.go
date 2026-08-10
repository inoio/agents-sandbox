package testutil

import "testing"

var installOrder []string //nolint:gochecknoglobals // populated by TestMain to verify InitMocks ordering

func TestMain(m *testing.M) {
	InitMocks(m,
		func() { installOrder = append(installOrder, "a") },
		func() { installOrder = append(installOrder, "b") },
	)
}

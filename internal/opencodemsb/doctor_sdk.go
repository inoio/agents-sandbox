//go:build cgo

package opencodemsb

import (
	"context"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

func CheckMsb(ctx context.Context) bool {
	if err := m.EnsureInstalled(ctx); err != nil {
		errorMsg("msb not found. Install microsandbox: https://github.com/microsandbox/microsandbox")
		return false
	}
	return true
}

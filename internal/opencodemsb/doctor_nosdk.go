//go:build !cgo

package opencodemsb

import (
	"context"
)

func CheckMsb(ctx context.Context) bool {
	warn("Skipping msb check: CGO is disabled.")
	return true
}

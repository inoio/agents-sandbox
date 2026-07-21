//go:build !cgo

package opencodemsb

import (
	"context"
	"errors"
)

func EnsureImage(ctx context.Context, dockerfile []byte, force bool) (imageRef, imageDigest string, err error) {
	return "", "", errors.New("docker build not available: CGO disabled and Docker client requires full implementation")
}

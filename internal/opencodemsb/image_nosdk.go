//go:build !cgo

package opencodemsb

import (
	"context"
	"errors"
)

func EnsureImage(ctx context.Context, dockerfile []byte, force bool) (imageRef, imageDigest string, err error) {
	return "", "", errors.New("image build requires CGO-enabled build with microsandbox SDK")
}

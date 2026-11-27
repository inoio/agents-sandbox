package msb

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// TestVolumeHandleFromPropagatesInfoError pins that a failed measurement
// observation fails the handle resolution, so a volume is not reported with
// silently zero measurements.
func TestVolumeHandleFromPropagatesInfoError(t *testing.T) {
	cause := errors.New("info boom")
	swapSeam(
		t,
		&realVolumeInfo,
		func(*msbSdk.Volume, context.Context) (*msbSdk.VolumeInfo, error) {
			return nil, cause
		},
	)

	if _, err := volumeHandleFrom(context.Background(), &msbSdk.Volume{}); !errors.Is(err, cause) {
		t.Fatalf("volumeHandleFrom() error = %v, want %v", err, cause)
	}
}

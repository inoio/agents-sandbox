package msb

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

// destinationSeam is one rebound sandbox/volume seam, named so a test can drive
// every seam and report which one failed.
type destinationSeam struct {
	name string
	call func() error
}

// destinationSeams is every seam the adapter rebound to the explicit local
// destination, so the destination tests cover all of them rather than the two
// getters.
func destinationSeams(ctx context.Context) []destinationSeam {
	return []destinationSeam{
		{"getSandbox", func() error { _, err := getSandbox(ctx, "example"); return err }},
		{"createSandbox", func() error { _, err := createSandbox(ctx, "example"); return err }},
		{"listSandboxesWith", func() error { _, err := listSandboxesWith(ctx); return err }},
		{"getVolume", func() error { _, err := getVolume(ctx, "example"); return err }},
		{"createVolume", func() error { _, err := createVolume(ctx, "example"); return err }},
		{"listVolumes", func() error { _, err := listVolumes(ctx); return err }},
		{"removeVolume", func() error { return removeVolume(ctx, "example") }},
	}
}

// TestLocalContextNamesThePinnedHome pins that the adapter's destination is an
// explicit LocalContext carrying the launcher's private home, not the ambient
// MSB_HOME. The zero context the fake returns has no native handle, so each
// collection call fails with ErrInvalidConfig, which is also the proof that the
// default seam entered the context's collection rather than a package-level
// function.
func TestLocalContextNamesThePinnedHome(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	var got msbSdk.LocalOptions
	swapSeam(t, &newLocalContext, func(options msbSdk.LocalOptions) (*msbSdk.LocalContext, error) {
		got = options
		return &msbSdk.LocalContext{}, nil
	})

	for _, seam := range destinationSeams(context.Background()) {
		if err := seam.call(); !msbSdk.IsKind(err, msbSdk.ErrInvalidConfig) {
			t.Errorf("%s() error = %v, want ErrInvalidConfig from the zero context", seam.name, err)
		}
	}
	if got.Home != configpaths.MSBHome() {
		t.Errorf("newLocalContext() Home = %q, want %q", got.Home, configpaths.MSBHome())
	}
}

// TestSandboxSeamsPropagateDestinationError pins that a failed destination
// resolution surfaces as every seam's error rather than panicking or
// dereferencing a nil context.
func TestSandboxSeamsPropagateDestinationError(t *testing.T) {
	cause := errors.New("no destination")
	swapSeam(t, &localContext, func() (*msbSdk.LocalContext, error) {
		return nil, cause
	})

	for _, seam := range destinationSeams(context.Background()) {
		if err := seam.call(); !errors.Is(err, cause) {
			t.Errorf("%s() error = %v, want %v", seam.name, err, cause)
		}
	}
}

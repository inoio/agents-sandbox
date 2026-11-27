package msb

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

// TestLocalContextNamesThePinnedHome pins that the adapter's destination is an
// explicit LocalContext carrying the launcher's private home, not the ambient
// MSB_HOME. The zero context the fake returns has no native handle, so the
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

	if _, err := getSandbox(context.Background(), "example"); !msbSdk.IsKind(err, msbSdk.ErrInvalidConfig) {
		t.Fatalf("getSandbox() error = %v, want ErrInvalidConfig from the zero context", err)
	}
	if _, err := getVolume(context.Background(), "example"); !msbSdk.IsKind(err, msbSdk.ErrInvalidConfig) {
		t.Fatalf("getVolume() error = %v, want ErrInvalidConfig from the zero context", err)
	}
	if got.Home != configpaths.MSBHome() {
		t.Errorf("newLocalContext() Home = %q, want %q", got.Home, configpaths.MSBHome())
	}
}

// TestSandboxSeamsPropagateDestinationError pins that a failed destination
// resolution surfaces as the seam's error rather than panicking or dereferencing
// a nil context.
func TestSandboxSeamsPropagateDestinationError(t *testing.T) {
	cause := errors.New("no destination")
	swapSeam(t, &localContext, func() (*msbSdk.LocalContext, error) {
		return nil, cause
	})

	if _, err := getSandbox(context.Background(), "example"); !errors.Is(err, cause) {
		t.Errorf("getSandbox() error = %v, want %v", err, cause)
	}
	if _, err := getVolume(context.Background(), "example"); !errors.Is(err, cause) {
		t.Errorf("getVolume() error = %v, want %v", err, cause)
	}
}

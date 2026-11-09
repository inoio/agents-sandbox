package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

var (
	errFirstEnsureInstalled  = errors.New("first ensure-installed seam")
	errSecondEnsureInstalled = errors.New("second ensure-installed seam")
	errFirstCheckDocker      = errors.New("first check-docker seam")
	errSecondCheckDocker     = errors.New("second check-docker seam")
)

// TestEnsureInstalledFailFastPanics pins that the ensure-installed seam still
// carries doctortest.InstallFailFast's panic after TestMain installs it. The
// equivalent CheckAll/CheckDocker assertions live in doctortest, but
// ensureMsbInstalled is unexported, so only a doctor-internal test can reach it.
func TestEnsureInstalledFailFastPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("ensureMsbInstalled should panic until a test mocks the seam")
		}
	}()
	_ = ensureMsbInstalled(context.Background())
}

// TestSetEnsureInstalledReturnsPrevious pins the setter contract doctortest
// relies on: it installs the given seam and returns the previous one, so a
// caller restores the previous seam by passing it back.
func TestSetEnsureInstalledReturnsPrevious(t *testing.T) {
	first := func(context.Context) error { return errFirstEnsureInstalled }
	second := func(context.Context) error { return errSecondEnsureInstalled }

	previous := SetEnsureInstalled(first)
	t.Cleanup(func() { SetEnsureInstalled(previous) })

	if got := ensureInstalledFunc(context.Background()); !errors.Is(got, errFirstEnsureInstalled) {
		t.Fatalf("first fn not in effect: %v", got)
	}

	if returned := SetEnsureInstalled(
		second,
	); returned == nil ||
		!errors.Is(returned(context.Background()), errFirstEnsureInstalled) {
		t.Fatal("setter did not return the previous fn")
	}
	if got := ensureInstalledFunc(context.Background()); !errors.Is(got, errSecondEnsureInstalled) {
		t.Fatalf("second fn not in effect: %v", got)
	}

	SetEnsureInstalled(first)
	if got := ensureInstalledFunc(context.Background()); !errors.Is(got, errFirstEnsureInstalled) {
		t.Fatalf("passing the previous fn back did not restore it: %v", got)
	}
}

// TestSetCheckAllReturnsPrevious pins the same contract for the CheckAll seam.
func TestSetCheckAllReturnsPrevious(t *testing.T) {
	first := func(context.Context, docker.Client, termio.Logger) bool { return true }
	second := func(context.Context, docker.Client, termio.Logger) bool { return false }

	previous := SetCheckAll(first)
	t.Cleanup(func() { SetCheckAll(previous) })

	if !checkAllFunc(context.Background(), nil, nil) {
		t.Fatal("first fn not in effect")
	}

	if returned := SetCheckAll(second); returned == nil || !returned(context.Background(), nil, nil) {
		t.Fatal("setter did not return the previous fn")
	}
	if checkAllFunc(context.Background(), nil, nil) {
		t.Fatal("second fn not in effect")
	}

	SetCheckAll(first)
	if !checkAllFunc(context.Background(), nil, nil) {
		t.Fatal("passing the previous fn back did not restore it")
	}
}

// TestSetCheckDockerReturnsPrevious pins the same contract for the CheckDocker
// seam.
func TestSetCheckDockerReturnsPrevious(t *testing.T) {
	first := func(context.Context, docker.Client) error { return errFirstCheckDocker }
	second := func(context.Context, docker.Client) error { return errSecondCheckDocker }

	previous := SetCheckDocker(first)
	t.Cleanup(func() { SetCheckDocker(previous) })

	if got := checkDockerFunc(context.Background(), nil); !errors.Is(got, errFirstCheckDocker) {
		t.Fatalf("first fn not in effect: %v", got)
	}

	if returned := SetCheckDocker(
		second,
	); returned == nil ||
		!errors.Is(returned(context.Background(), nil), errFirstCheckDocker) {
		t.Fatal("setter did not return the previous fn")
	}
	if got := checkDockerFunc(context.Background(), nil); !errors.Is(got, errSecondCheckDocker) {
		t.Fatalf("second fn not in effect: %v", got)
	}

	SetCheckDocker(first)
	if got := checkDockerFunc(context.Background(), nil); !errors.Is(got, errFirstCheckDocker) {
		t.Fatalf("passing the previous fn back did not restore it: %v", got)
	}
}

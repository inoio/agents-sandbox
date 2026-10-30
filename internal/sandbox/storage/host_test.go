package storage

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

// lockedRuntimeHome returns a private home whose bin subtree cannot be read.
func lockedRuntimeHome(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block directory reads")
	}
	home := t.TempDir()
	locked := filepath.Join(home, "bin", "locked")
	writeFile(t, filepath.Join(locked, "msb"), 10)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("Chmod error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	return home
}

func TestObserveRuntimeSumsBinAndLib(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "bin", "msb"), 100)
	writeFile(t, filepath.Join(home, "lib", "libkrunfw.so.5.6.1"), 200)

	category, err := observeRuntime(home, Options{})
	if err != nil {
		t.Fatalf("observeRuntime() error = %v", err)
	}
	if category.Name != CategoryRuntime {
		t.Errorf("name = %s, want %s", category.Name, CategoryRuntime)
	}
	if got := category.Totals.Count; got == nil || *got != 2 {
		t.Errorf("count = %v, want 2", got)
	}
	if got := category.Totals.LogicalBytes; got == nil || *got != 300 {
		t.Errorf("logical bytes = %v, want 300", got)
	}
	if category.Totals.AllocatedBytes != nil || category.Totals.ReclaimableBytes != nil {
		t.Errorf("allocated/reclaimable = %v/%v, want nil (not observed)",
			category.Totals.AllocatedBytes, category.Totals.ReclaimableBytes)
	}
	if category.Objects != nil {
		t.Errorf("objects = %v, want nil (not requested)", category.Objects)
	}
}

func TestObserveRuntimeMissingHomeIsKnownZero(t *testing.T) {
	category, err := observeRuntime(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("observeRuntime() error = %v", err)
	}
	if got := category.Totals.Count; got == nil || *got != 0 {
		t.Errorf("count = %v, want known zero", got)
	}
	if got := category.Totals.LogicalBytes; got == nil || *got != 0 {
		t.Errorf("logical bytes = %v, want known zero", got)
	}
}

func TestObserveRuntimeNonDirHomeErrorPropagates(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if _, err := observeRuntime(file, Options{}); !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("observeRuntime() with a regular file as home error = %v, want ENOTDIR", err)
	}
}

func TestObserveRuntimeWalkErrorPropagates(t *testing.T) {
	if _, err := observeRuntime(lockedRuntimeHome(t), Options{}); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("observeRuntime() error = %v, want fs.ErrPermission from the unreadable subtree", err)
	}
}

func TestObservePropagatesRuntimeWalkError(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return &msbSdk.StorageUsageReport{}, nil
	}})
	home := lockedRuntimeHome(t)
	stubPrivateHome(t, home)

	if _, err := Observe(context.Background(), Options{}); err == nil {
		t.Fatal("Observe() error = nil, want propagation of runtime walk error")
	}
}

func TestObserveRuntimeSkipsNonRegularEntries(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "bin", "msb"), 32)
	if err := os.Symlink(filepath.Join(home, "bin", "msb"), filepath.Join(home, "bin", "link")); err != nil {
		t.Fatalf("Symlink error = %v", err)
	}

	category, err := observeRuntime(home, Options{})
	if err != nil {
		t.Fatalf("observeRuntime() error = %v", err)
	}
	if got := category.Totals.Count; got == nil || *got != 1 {
		t.Errorf("count = %v, want 1 (symlink skipped)", got)
	}
}

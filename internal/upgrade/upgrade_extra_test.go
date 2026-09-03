package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestUpdateExecutable exercises updateExecutable end to end in a child
// process, since it atomically replaces the running executable. The child
// re-runs this test binary with a helper marker and replaces its own binary
// with the served asset.
func TestUpdateExecutable(t *testing.T) {
	if os.Getenv("UPGRADE_HELPER_PROCESS") == "1" {
		downloadBase = os.Getenv("UPGRADE_DOWNLOAD_BASE")
		if err := updateExecutable(context.Background(), ""); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("#!/bin/sh\necho new\n"))
	}))
	t.Cleanup(srv.Close)

	cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateExecutable$")
	cmd.Env = append(os.Environ(),
		"UPGRADE_HELPER_PROCESS=1",
		"UPGRADE_DOWNLOAD_BASE="+srv.URL,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}
}

func TestUpdateExecutableDownloadFailure(t *testing.T) {
	if os.Getenv("UPGRADE_HELPER_PROCESS") == "1" {
		downloadBase = "http://127.0.0.1:1"
		if err := updateExecutable(context.Background(), ""); err == nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateExecutableDownloadFailure$")
	cmd.Env = append(os.Environ(), "UPGRADE_HELPER_PROCESS=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}
}

func TestDownloadAssetCreateTempFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := downloadAssetToDir(context.Background(), dir); err == nil {
		t.Fatal("expected error when the destination directory does not exist")
	}
}

func TestDownloadAssetCopyFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	if _, err := downloadAssetToDir(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected error when the response body is truncated")
	}
}

func TestReplaceExecutableChmodFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "opencode-sandbox")
	_ = os.WriteFile(target, []byte("old"), 0o755)

	asset := filepath.Join(dir, "missing-asset")
	if err := replaceExecutable(asset, target); err == nil {
		t.Fatal("expected error when the asset cannot be chmod'd")
	}
}

func TestReplaceExecutableRenameFails(t *testing.T) {
	dir := t.TempDir()
	asset := filepath.Join(dir, "asset")
	_ = os.WriteFile(asset, []byte("new"), 0o600)

	exePath := filepath.Join(dir, "opencode-sandbox")
	_ = os.MkdirAll(exePath, 0o755)

	if err := replaceExecutable(asset, exePath); err == nil {
		t.Fatal("expected error when the rename target is a directory")
	}
}

func TestSaveStateMkdirAllFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)

	path := filepath.Join(blocker, "state.json")
	if err := saveState(path, state{}); err == nil {
		t.Fatal("expected error when the state directory cannot be created")
	}
}

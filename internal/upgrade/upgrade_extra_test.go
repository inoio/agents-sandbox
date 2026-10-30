package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// copyTestBinary copies the running test binary into a fresh temp directory and
// returns the copy's path. A child-process test replaces the executable it runs,
// so each must run its own copy: replacing the shared test binary would make
// every later child exec the replacement instead of the test.
func copyTestBinary(t *testing.T) string {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatalf("open test binary: %v", err)
	}
	defer source.Close()

	target := filepath.Join(t.TempDir(), "upgrade.test")
	dest, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create test binary copy: %v", err)
	}
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		t.Fatalf("copy test binary: %v", err)
	}
	if err := dest.Close(); err != nil {
		t.Fatalf("close test binary copy: %v", err)
	}
	return target
}

// TestUpdateExecutable exercises updateExecutable end to end in a child
// process, since it atomically replaces the running executable. The child
// re-runs a copy of this test binary with a helper marker and replaces its own
// binary with the served asset.
func TestUpdateExecutable(t *testing.T) {
	if os.Getenv("UPGRADE_HELPER_PROCESS") == "1" {
		downloadBase = os.Getenv("UPGRADE_DOWNLOAD_BASE")
		if err := updateExecutable(context.Background(), "2.0.0"); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	asset := []byte("#!/bin/sh\necho new\n")
	digest := sha256.Sum256(asset)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2.0.0/"+checksumsAsset {
			_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(digest[:]), assetName())
			return
		}
		_, _ = w.Write(asset)
	}))
	t.Cleanup(srv.Close)

	childBinary := copyTestBinary(t)
	cmd := exec.Command(childBinary, "-test.run=^TestUpdateExecutable$")
	cmd.Env = append(os.Environ(),
		"UPGRADE_HELPER_PROCESS=1",
		"UPGRADE_DOWNLOAD_BASE="+srv.URL,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}

	installed, err := os.ReadFile(childBinary)
	if err != nil {
		t.Fatalf("read child binary: %v", err)
	}
	if !bytes.Equal(installed, asset) {
		t.Fatalf("child binary content = %q, want %q", installed, asset)
	}
}

func TestUpdateExecutableDownloadFailure(t *testing.T) {
	if os.Getenv("UPGRADE_HELPER_PROCESS") == "1" {
		downloadBase = "http://127.0.0.1:1"
		if err := updateExecutable(context.Background(), "2.0.0"); err == nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cmd := exec.Command(copyTestBinary(t), "-test.run=^TestUpdateExecutableDownloadFailure$")
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
	if _, err := downloadAssetToDir(context.Background(), dir, "2.0.0"); err == nil {
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

	if _, err := downloadAssetToDir(context.Background(), t.TempDir(), "2.0.0"); err == nil {
		t.Fatal("expected error when the response body is truncated")
	}
}

func TestReplaceExecutableChmodFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agents-sandbox")
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

	exePath := filepath.Join(dir, "agents-sandbox")
	_ = os.MkdirAll(exePath, 0o755)

	if err := replaceExecutable(asset, exePath); err == nil {
		t.Fatal("expected error when the rename target is a directory")
	}
}

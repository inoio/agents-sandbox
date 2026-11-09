//go:build integration

package volume

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// runCopyTree runs copyTreeCommand with the host's sh and tar, the way the copy
// sandbox runs it, and returns the command's error and combined output.
func runCopyTree(t *testing.T, source, target string) (string, error) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "source-failed")
	output, err := exec.Command("sh", "-c", copyTreeCommand(source, target, marker)).CombinedOutput()
	return string(output), err
}

func TestCopyTreeCommand_CopiesModesTimestampsAndSymlinksOntoExistingFiles(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	secret := filepath.Join(source, "secret")
	if err := os.WriteFile(secret, []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(secret, modified, modified); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin", "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin/tool", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	// The prefill leaves the image's copies in the target with other modes;
	// the user's modes must win.
	if err := os.WriteFile(filepath.Join(target, "secret"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "bin", "tool"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	if output, err := runCopyTree(t, source, target); err != nil {
		t.Fatalf("copy failed: %v\n%s", err, output)
	}

	assertMode(t, filepath.Join(target, "secret"), 0o600)
	assertMode(t, filepath.Join(target, "bin", "tool"), 0o755)
	if content, _ := os.ReadFile(filepath.Join(target, "secret")); string(content) != "user" {
		t.Errorf("secret content = %q, want the source's", content)
	}
	if info, err := os.Stat(filepath.Join(target, "secret")); err != nil || !info.ModTime().Equal(modified) {
		t.Errorf("secret mtime = %v (err %v), want %v", info.ModTime(), err, modified)
	}
	if link, err := os.Readlink(filepath.Join(target, "link")); err != nil || link != "bin/tool" {
		t.Errorf("link = %q (err %v), want a symlink to bin/tool", link, err)
	}
}

func TestCopyTreeCommand_KeepsSparseFilesSparse(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	const apparentSize = 256 << 20
	image, err := os.Create(filepath.Join(source, "disk.img"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := image.WriteAt([]byte("x"), apparentSize/2); err != nil {
		t.Fatal(err)
	}
	if err := image.Truncate(apparentSize); err != nil {
		t.Fatal(err)
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
	if allocatedBytes(t, filepath.Join(source, "disk.img")) >= apparentSize/2 {
		t.Skip("the temp directory's filesystem does not keep holes")
	}

	if output, err := runCopyTree(t, source, target); err != nil {
		t.Fatalf("copy failed: %v\n%s", err, output)
	}

	if allocated := allocatedBytes(t, filepath.Join(target, "disk.img")); allocated >= apparentSize/2 {
		t.Errorf("copied sparse file allocates %d bytes of %d; its holes were written out", allocated, apparentSize)
	}
}

func allocatedBytes(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("allocated size is not available on this platform")
	}
	return stat.Blocks * 512
}

func TestCopyTreeCommand_FailsWhenTheSourceCannotBeArchived(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file, so the archive cannot be made to fail this way")
	}
	source, target := t.TempDir(), t.TempDir()
	unreadable := filepath.Join(source, "unreadable")
	if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "readable"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// tar still writes a valid archive of the readable files and only exits
	// non-zero, so without the marker the extract would succeed.
	if output, err := runCopyTree(t, source, target); err == nil {
		t.Fatalf("copy succeeded with an unreadable source file; output:\n%s", output)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %v, want %v", filepath.Base(path), got, want)
	}
}

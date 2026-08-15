//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/doctor"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func expectDoctorFailure(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected doctor to report a preflight failure; got nil error")
	}
	if !strings.Contains(err.Error(), "preflight failed") {
		t.Errorf("expected error 'preflight failed'; got: %v", err)
	}
}

func expectDoctorSuccess(t *testing.T, ui *termio.Mock, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected doctor to pass; got error: %v", err)
	}
	found := false
	for _, call := range ui.InfoCalls {
		if strings.TrimSpace(call) == "doctor: all checks passed" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected info 'doctor: all checks passed'; got: %v", ui.InfoCalls)
	}
}
func TestDoctor_AllChecksFail(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "doctor")
	doctor.MockedCheckAll(t, false)

	err := cmd.Execute()

	expectDoctorFailure(t, err)
}

func TestDoctor_AllChecksPass(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "doctor")
	doctor.MockedEnsureInstalled(t, false)

	// msb must be resolvable for a clean pass: put a fake msb binary on PATH.
	binDir := t.TempDir()
	for _, name := range []string{"docker", "git", "msb"} {
		f, err := os.OpenFile(filepath.Join(binDir, name), os.O_CREATE|os.O_WRONLY, 0o755)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("#!/bin/sh\n"); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	err := cmd.Execute()

	expectDoctorSuccess(t, ui, err)
}

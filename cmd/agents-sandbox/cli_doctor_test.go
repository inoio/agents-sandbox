//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/termio"
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
func TestDoctorAllChecksFail(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "doctor")
	doctor.MockedCheckAll(t, false)

	err := cmd.Execute()

	expectDoctorFailure(t, err)
}

func TestDoctorMobyReportsDocker(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "doctor")
	doctor.MockedRealCheckAll(t)
	doctor.MockedEnsureInstalled(t, false)
	t.Setenv("AGENTS_SANDBOX_BUILDER", "moby")
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	t.Setenv("HOME", t.TempDir())

	binDir := t.TempDir()
	f, err := os.OpenFile(filepath.Join(binDir, "msb"), os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("#!/bin/sh\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Setenv("PATH", binDir)

	err = cmd.Execute()

	expectDoctorFailure(t, err)
	messages := make([]string, 0, len(ui.ErrorCalls))
	for _, call := range ui.ErrorCalls {
		messages = append(messages, call.Msg)
	}
	if !strings.Contains(strings.ToLower(strings.Join(messages, "\n")), "docker") {
		t.Fatalf("expected a docker prerequisite error, got %q", messages)
	}
}

func TestDoctorAllChecksPass(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "doctor")
	doctor.MockedEnsureInstalled(t, false)

	// msb must be resolvable for a clean pass: put a fake msb binary on PATH.
	binDir := t.TempDir()
	for _, name := range []string{"msb"} {
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

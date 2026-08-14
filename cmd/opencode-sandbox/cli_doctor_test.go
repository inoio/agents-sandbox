//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/doctor"
	sandboxmsb "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// renderDoctorErrorCall mirrors termio.printer.Error, rendering "msg: err". It
// lets tests assert on both the guidance text and the underlying error.
func renderDoctorErrorCall(e termio.ErrorCall) string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

// joinDoctorErrors renders every error the mock UI captured as a single string.
func joinDoctorErrors(ui *termio.Mock) string {
	parts := make([]string, 0, len(ui.ErrorCalls))
	for _, e := range ui.ErrorCalls {
		parts = append(parts, renderDoctorErrorCall(e))
	}
	return strings.Join(parts, " ")
}

// setupDoctorTest drives the real doctor.CheckAll pipeline with all
// prerequisites satisfied and all external dependencies mocked. Tests then
// break exactly one prerequisite to assert on its detailed error message.
// It returns the msb mock so tests can seed orphaned handles.
func setupDoctorTest(t *testing.T, kvmOK bool) *sandboxmsb.MockMsbClient {
	t.Helper()
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)

	msbMock := &sandboxmsb.MockMsbClient{}
	sandboxmsb.WithMsbMock(t, msbMock)
	docker.WithNoopDockerMock(t)

	// Point checkKvm at a controllable path so it never depends on the host.
	origKvm := doctor.KvmPath
	t.Cleanup(func() { doctor.KvmPath = origKvm })
	if kvmOK {
		kvmFile := filepath.Join(t.TempDir(), "kvm")
		if err := os.WriteFile(kvmFile, nil, 0o600); err != nil {
			t.Fatalf("create fake kvm device: %v", err)
		}
		doctor.KvmPath = kvmFile
	} else {
		doctor.KvmPath = filepath.Join(t.TempDir(), "missing-kvm")
	}

	return msbMock
}

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

// D1: KVM device missing. The opener /dev/kvm message must explain how to fix it.
func TestDoctor_KvmMissing(t *testing.T) {
	ui := &termio.Mock{}
	setupDoctorTest(t, false)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()

	expectDoctorFailure(t, err)
	out := joinDoctorErrors(ui)
	if !strings.Contains(out, "/dev/kvm not found") {
		t.Errorf("expected '/dev/kvm not found'; got %q", out)
	}
	if !strings.Contains(out, "Load kvm module") || !strings.Contains(out, "kvm group") {
		t.Errorf("expected fix guidance 'Load kvm module and ensure user is in the kvm group'; got %q", out)
	}
}

// D2: Docker API unreachable. Must report the ping error and how to start Docker.
func TestDoctor_DockerAPIUnreachable(t *testing.T) {
	ui := &termio.Mock{}
	setupDoctorTest(t, true)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		PingFn: func(context.Context, client.PingOptions) (client.PingResult, error) {
			return client.PingResult{}, errors.New("connect: permission denied")
		},
	})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()

	expectDoctorFailure(t, err)
	out := joinDoctorErrors(ui)
	if !strings.Contains(out, "Docker API unreachable") {
		t.Errorf("expected 'Docker API unreachable'; got %q", out)
	}
	if !strings.Contains(out, "connect: permission denied") {
		t.Errorf("expected underlying ping error; got %q", out)
	}
	foundHint := false
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, "Ensure Docker Desktop or colima is running") {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Errorf("expected Docker start hint; got InfoCalls: %v", ui.InfoCalls)
	}
}

// D3: docker binary missing from PATH. Must explain how to install Docker/Podman.
func TestDoctor_DockerBinaryMissing(t *testing.T) {
	ui := &termio.Mock{}
	setupDoctorTest(t, true)

	// PATH with no docker binary so the docker lookup fails.
	t.Setenv("PATH", t.TempDir())

	root := buildRootCmd(ui)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()

	expectDoctorFailure(t, err)
	out := joinDoctorErrors(ui)
	if !strings.Contains(out, "docker not found") {
		t.Errorf("expected 'docker not found'; got %q", out)
	}
	if !strings.Contains(out, "Install Docker or Podman with docker-compatible CLI") {
		t.Errorf("expected fix guidance 'Install Docker or Podman'; got %q", out)
	}
}

// D4: git binary missing. Must explain how to install git.
func TestDoctor_GitMissing(t *testing.T) {
	ui := &termio.Mock{}
	setupDoctorTest(t, true)

	// PATH with docker present but git absent, so the git lookup fails.
	binDir := t.TempDir()
	for _, name := range []string{"docker"} {
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

	root := buildRootCmd(ui)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()

	expectDoctorFailure(t, err)
	out := joinDoctorErrors(ui)
	if !strings.Contains(out, "git not found") {
		t.Errorf("expected 'git not found'; got %q", out)
	}
	if !strings.Contains(out, "Install git via your system package manager") {
		t.Errorf("expected git install guidance; got %q", out)
	}
}

// D5: microsandbox runtime auto-install fails. Must surface the setup error.
func TestDoctor_MsbInstallFailed(t *testing.T) {
	ui := &termio.Mock{}
	setupDoctorTest(t, true)

	origCheck := doctor.SetEnsureInstalled(func(context.Context) error {
		return errors.New("network unreachable")
	})
	t.Cleanup(func() { doctor.SetEnsureInstalled(origCheck) })

	root := buildRootCmd(ui)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()

	expectDoctorFailure(t, err)
	out := joinDoctorErrors(ui)
	if !strings.Contains(out, "msb runtime setup failed") {
		t.Errorf("expected 'msb runtime setup failed'; got %q", out)
	}
	if !strings.Contains(out, "microsandbox runtime could not be auto-installed") {
		t.Errorf("expected 'microsandbox runtime could not be auto-installed'; got %q", out)
	}
	if !strings.Contains(out, "network unreachable") {
		t.Errorf("expected underlying install error; got %q", out)
	}
}

// D6: msb installed but its binary is missing off PATH. Must tell the user where.
func TestDoctor_MsbBinaryMissing(t *testing.T) {
	ui := &termio.Mock{}
	setupDoctorTest(t, true)

	origCheck := doctor.SetEnsureInstalled(func(context.Context) error { return nil })
	t.Cleanup(func() { doctor.SetEnsureInstalled(origCheck) })

	// msb installs to HOME/.microsandbox/bin; make HOME and PATH contain no msb.
	home := t.TempDir()
	t.Setenv("HOME", home)

	binDir := t.TempDir()
	for _, name := range []string{"docker", "git"} {
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

	root := buildRootCmd(ui)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()

	expectDoctorFailure(t, err)
	out := joinDoctorErrors(ui)
	wantBinDir := filepath.Join(home, ".microsandbox", "bin")
	if !strings.Contains(out, "msb not on PATH and binary missing at "+wantBinDir) {
		t.Errorf("expected 'msb not on PATH and binary missing at %s'; got %q", wantBinDir, out)
	}
}

// D7: orphaned sandboxes/volumes/images are surfaced as actionable warnings.
func TestDoctor_OrphanedArtifacts(t *testing.T) {
	ui := &termio.Mock{}
	msbMock := setupDoctorTest(t, true)

	msbMock.Sandboxes = []sandboxmsb.SandboxHandle{
		&sandboxmsb.MockSandboxHandle{Name_: "opencode-sandbox-sb-proj-main"},
	}
	msbMock.Volumes = []sandboxmsb.VolumeHandle{
		sandboxmsb.MockVolumeHandle{Name_: "opencode-sandbox-clone-proj-x-123"},
	}
	msbMock.Images = []sandboxmsb.ImageHandle{
		sandboxmsb.MockImageHandle{Reference_: "opencode-sandbox/runner:base"},
	}

	root := buildRootCmd(ui)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()

	expectDoctorFailure(t, err)
	for _, want := range []string{
		"Found orphaned sandbox: opencode-sandbox-sb-proj-main",
		"Found orphaned volume: opencode-sandbox-clone-proj-x-123",
		"Found orphaned image: opencode-sandbox/runner:base",
	} {
		found := false
		for _, call := range ui.WarnCalls {
			if strings.Contains(call, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected warning containing %q; got: %v", want, ui.WarnCalls)
		}
	}
}

// D8: all prerequisites OK reports a clean pass.
func TestDoctor_AllChecksPass(t *testing.T) {
	ui := &termio.Mock{}
	setupDoctorTest(t, true)

	origCheck := doctor.SetEnsureInstalled(func(context.Context) error { return nil })
	t.Cleanup(func() { doctor.SetEnsureInstalled(origCheck) })

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

	root := buildRootCmd(ui)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()

	expectDoctorSuccess(t, ui, err)
}

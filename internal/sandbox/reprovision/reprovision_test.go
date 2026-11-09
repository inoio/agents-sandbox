package reprovision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

func TestProvisionWritesOpenCodeAndHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte("{\n  \"model\": \"x\"\n}\n"),
		MergedPath:  opencodeMergedPath,
		HomeFiles: map[string][]byte{
			"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
		},
	}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{FSValue_: fs}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if fs.Writes[opencodeMergedPath] == nil {
		t.Error("expected opencode.json to be written")
	}
	if fs.Writes["/home/dev/.config/tool/cfg.toml"] == nil {
		t.Error("expected home file to be written")
	}
}

func TestProvisionNoSnippetsSkipsOpenCode(t *testing.T) {
	cf := &ConfigFiles{Merged: nil, HomeFiles: map[string][]byte{}}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{FSValue_: fs}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, ok := fs.Writes[opencodeMergedPath]; ok {
		t.Error("did not expect opencode.json when HasSnippets=false")
	}
}

// TestProvisionChownsHomeFiles verifies that Provision writes the home files and
// the merged opencode config, then chowns every provisioned path to dev:dev so
// the files are readable by the runtime user.
func TestProvisionChownsHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig":            []byte("user.name=X\n"),
			"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
		},
		Modes: map[string]os.FileMode{"/home/dev/.gitconfig": 0o755},
	}
	fs := msbtest.NewTestFS(nil)
	var calls []string
	var execCalls []string
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &calls, ExecCalls: &execCalls}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	for path := range cf.HomeFiles {
		if _, ok := fs.Writes[path]; !ok {
			t.Errorf("home file %s was not written", path)
		}
	}
	if _, ok := fs.Writes[opencodeMergedPath]; !ok {
		t.Error("opencode config was not written")
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 chown shell call, got %d: %v", len(calls), calls)
	}
	wantChown := "chown -R dev:dev /home /home/dev /home/dev/.config /home/dev/.config/opencode " +
		"/home/dev/.config/opencode/opencode.jsonc /home/dev/.config/tool /home/dev/.config/tool/cfg.toml " +
		"/home/dev/.gitconfig"
	if calls[0] != wantChown {
		t.Errorf("shell command = %q, want %q", calls[0], wantChown)
	}
	if !slices.Contains(execCalls, "chmod 0755 /home/dev/.gitconfig") {
		t.Errorf("exec calls = %v, want home-file chmod", execCalls)
	}
}

func TestProvisionReturnsHomeChmodError(t *testing.T) {
	path := "/home/dev/.gitconfig"
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{path: []byte("user.name=X\n")},
		Modes:     map[string]os.FileMode{path: 0o755},
	}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{
		FSValue_: fs,
		ExecOut: map[string]msb.ShellResult{
			"chmod 0755 " + path: msbtest.NewTestResult(false, 1, "", "chmod failed", nil),
		},
	}

	err := Provision(context.Background(), sb, cf)
	if err == nil || !strings.Contains(err.Error(), "chmod home file") {
		t.Fatalf("Provision error = %v, want home-file chmod error", err)
	}
}

// TestProvisionNoFilesSkipsChown verifies that Provision does not run a chown
// when there are no files to provision.
func TestProvisionNoFilesSkipsChown(t *testing.T) {
	var calls []string
	sb := &msbtest.MockSandbox{FSValue_: msbtest.NewTestFS(nil), ShellCalls: &calls}
	if err := Provision(context.Background(), sb, &ConfigFiles{}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("expected no shell call, got %v", calls)
	}
}

// TestProvisionJoinsErrors verifies that when a write fails and the deferred
// chown also fails, both errors are surfaced via errors.Join.
func TestProvisionJoinsErrors(t *testing.T) {
	writeErr := errors.New("write boom")
	chownErr := errors.New("chown boom")
	cf := &ConfigFiles{HomeFiles: map[string][]byte{"/home/dev/.gitconfig": []byte("x\n")}}
	fs := msbtest.NewTestFS(nil)
	fs.WriteErr = writeErr
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellErr: chownErr}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, writeErr) {
		t.Errorf("Provision error does not wrap write error: %v", err)
	}
	if !errors.Is(err, chownErr) {
		t.Errorf("Provision error does not wrap chown error: %v", err)
	}
}

// TestProvisionRemovesStalePaths verifies that Provision removes the marked
// stale paths before writing the merged config.
func TestProvisionRemovesStalePaths(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		Remove: configFileFamilyPaths(
			opencodeMergedPath,
			resolveConfigSubject(agent.Resolve(opencodeTestAgent()), VMHomeDir).familyNames,
		),
	}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(fs.Removed) != len(cf.Remove) {
		t.Fatalf("expected %d removals, got %v", len(cf.Remove), fs.Removed)
	}
	for i, p := range cf.Remove {
		if i >= len(fs.Removed) || fs.Removed[i] != p {
			t.Errorf("Remove order = %v, want %v", fs.Removed, cf.Remove)
			break
		}
	}
	if fs.Writes[opencodeMergedPath] == nil {
		t.Error("expected merged config written after removal")
	}
}

// TestProvisionWritesProvisioned verifies that Provision writes the default
// drop-in copy just like home files.
func TestProvisionWritesProvisioned(t *testing.T) {
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{
			"/home/dev/.config/opencode/opencode.json":  []byte(`{"a":1}`),
			"/home/dev/.local/share/opencode/auth.json": []byte(`{"t":"x"}`),
		},
	}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for p := range cf.Provisioned {
		if _, ok := fs.Writes[p]; !ok {
			t.Errorf("provisioned file %s was not written", p)
		}
	}
}

func TestProvisionAppliesProvisionedFileMode(t *testing.T) {
	path := "/home/dev/.config/opencode/bin/launcher"
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{path: []byte("#!/bin/sh\n")},
		Modes:       map[string]os.FileMode{path: 0o755},
	}
	fs := msbtest.NewTestFS(nil)
	var execCalls []string
	sb := &msbtest.MockSandbox{
		FSValue_:  fs,
		ExecCalls: &execCalls,
	}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !slices.Contains(execCalls, "chmod 0755 "+path) {
		t.Errorf("exec calls = %v, want chmod for %s", execCalls, path)
	}
}

func TestProvisionReturnsChmodError(t *testing.T) {
	path := "/home/dev/.config/opencode/bin/launcher"
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{path: []byte("#!/bin/sh\n")},
		Modes:       map[string]os.FileMode{path: 0o755},
	}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{
		FSValue_: fs,
		ExecOut: map[string]msb.ShellResult{
			"chmod 0755 " + path: msbtest.NewTestResult(false, 1, "", "chmod failed", nil),
		},
	}

	err := Provision(context.Background(), sb, cf)
	if err == nil || !strings.Contains(err.Error(), "chmod") {
		t.Fatalf("Provision error = %v, want chmod error", err)
	}
}

func TestProvisionReturnsChmodExecError(t *testing.T) {
	path := "/home/dev/.config/opencode/bin/launcher"
	wantErr := errors.New("exec failed")
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{path: []byte("#!/bin/sh\n")},
		Modes:       map[string]os.FileMode{path: 0o755},
	}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{FSValue_: fs, ExecErr: wantErr}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Provision error = %v, want %v", err, wantErr)
	}
}

func TestProvisionReturnsMirrorChmodError(t *testing.T) {
	path := "/home/dev/.config/opencode/bin/launcher"
	cf := &ConfigFiles{
		Mirror: map[string][]byte{path: []byte("#!/bin/sh\n")},
		Modes:  map[string]os.FileMode{path: 0o755},
	}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{
		FSValue_: fs,
		ExecOut: map[string]msb.ShellResult{
			"chmod 0755 " + path: msbtest.NewTestResult(false, 1, "", "chmod failed", nil),
		},
	}

	err := Provision(context.Background(), sb, cf)
	if err == nil || !strings.Contains(err.Error(), "chmod mirror file") {
		t.Fatalf("Provision error = %v, want mirror-file chmod error", err)
	}
}

// TestProvisionWritesMirror verifies that Provision writes the verbatim mirror
// files just like home files and drop-in copies.
func TestProvisionWritesMirror(t *testing.T) {
	launcherPath := "/home/dev/.config/opencode/bin/launcher"
	cf := &ConfigFiles{
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json":        []byte(`{"theme":"dark"}`),
			"/home/dev/.config/opencode/agents/coder.md": []byte("# coder\n"),
			launcherPath: []byte("#!/bin/sh\n"),
		},
		Modes: map[string]os.FileMode{launcherPath: 0o755},
	}
	fs := msbtest.NewTestFS(nil)
	var execCalls []string
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}, ExecCalls: &execCalls}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for p := range cf.Mirror {
		if _, ok := fs.Writes[p]; !ok {
			t.Errorf("mirror file %s was not written", p)
		}
	}
	if !slices.Contains(execCalls, "chmod 0755 "+launcherPath) {
		t.Errorf("exec calls = %v, want chmod for %s", execCalls, launcherPath)
	}
}

// TestProvisionPropagatesMkdirError verifies a failure creating the merged
// config's parent directory aborts provisioning instead of silently continuing.
func TestProvisionPropagatesMkdirError(t *testing.T) {
	mkdirErr := errors.New("mkdir boom")
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  filepath.Join(VMHomeDir, ".config", "opencode", "opencode.jsonc"),
	}
	fs := msbtest.NewTestFS(nil)
	fs.MkdirErr = mkdirErr
	sb := &msbtest.MockSandbox{FSValue_: fs}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, mkdirErr) {
		t.Errorf("expected mkdir error to be surfaced, got %v", err)
	}
}

// TestProvisionWriteOpenCodeError verifies that a write failure for the
// opencode config surfaces an error.
func TestProvisionWriteOpenCodeError(t *testing.T) {
	writeErr := errors.New("write boom")
	cf := &ConfigFiles{HasSnippets: true, Merged: []byte(`{"model":"x"}`)}
	fs := msbtest.NewTestFS(nil)
	fs.WriteErr = writeErr
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellErr: nil}
	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, writeErr) {
		t.Errorf("expected write error to be surfaced, got %v", err)
	}
}

// TestProvisionReturnsMkdirError verifies that a failure creating a parent
// directory surfaces from Provision.
func TestProvisionReturnsMkdirError(t *testing.T) {
	mkdirErr := errors.New("mkdir boom")
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v\n")},
	}
	fs := msbtest.NewTestFS(nil)
	fs.MkdirErr = mkdirErr
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, mkdirErr) {
		t.Fatalf("Provision error = %v, want %v", err, mkdirErr)
	}
}

func TestProvisionReturnsErrorWhenProvisionedWriteFails(t *testing.T) {
	writeErr := errors.New("provisioned write boom")
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v\n")},
	}
	fs := msbtest.NewTestFS(nil)
	fs.WriteErr = writeErr
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, writeErr) {
		t.Errorf("Provision error = %v, want to wrap %v", err, writeErr)
	}
}

// TestChownPathsReportsUnsuccessfulShell verifies the error branch when the
// chown shell command reports a failed (non-zero) exit status.
func TestChownPathsReportsUnsuccessfulShell(t *testing.T) {
	cmd := "chown -R dev:dev /a /b"
	sb := &msbtest.MockSandbox{
		FSValue_: msbtest.NewTestFS(nil),
		ShellOut: map[string]msb.ShellResult{
			cmd: msbtest.NewTestResult(false, 1, "", "permission denied", nil),
		},
	}
	err := chownPaths(context.Background(), sb, []string{"/b", "/a"})
	if err == nil {
		t.Error("expected an error when chown reports a failed exit status")
	}
}

// TestMkdirAllFSExisting verifies that an existing directory short-circuits.
func TestMkdirAllFSExisting(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{"/home/dev/.config/opencode": nil})
	made, err := mkdirAllFS(context.Background(), fs, "/home/dev/.config/opencode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(made) != 0 {
		t.Errorf("expected no dirs created for existing path, got %v", made)
	}
}

// TestMkdirAllFSRootPath verifies the root/empty/`.` short-circuit branches.
func TestMkdirAllFSRootPath(t *testing.T) {
	fs := msbtest.NewTestFS(nil)
	for _, p := range []string{"", "/", "."} {
		made, err := mkdirAllFS(context.Background(), fs, p)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", p, err)
		}
		if len(made) != 0 {
			t.Errorf("expected no dirs for %q, got %v", p, made)
		}
	}
}

// pathFailingFS fails Mkdir and Write for one path only, so a test can stop
// provisioning midway.
type pathFailingFS struct {
	*msbtest.TestFS

	failPath string
}

func (f pathFailingFS) Mkdir(ctx context.Context, path string) error {
	if path == f.failPath {
		return errors.New("mkdir boom")
	}
	return f.TestFS.Mkdir(ctx, path)
}

func (f pathFailingFS) Write(ctx context.Context, path string, data []byte) error {
	if path == f.failPath {
		return errors.New("write boom")
	}
	return f.TestFS.Write(ctx, path, data)
}

// TestProvisionChownsWhatItCreatedBeforeAFailure pins the finalizer: files
// written and directories created before a failure are still chowned to the
// runtime user, so no root-owned leftovers remain.
func TestProvisionChownsWhatItCreatedBeforeAFailure(t *testing.T) {
	cases := []struct {
		name      string
		cf        *ConfigFiles
		failPath  string
		execErr   error
		wantChown string
	}{
		{
			name:      "an intermediate directory creation fails",
			cf:        &ConfigFiles{Provisioned: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v\n")}},
			failPath:  "/home/dev",
			wantChown: "chown -R dev:dev /home",
		},
		{
			name: "chmod after a write fails",
			cf: &ConfigFiles{
				HomeFiles: map[string][]byte{"/home/dev/run.sh": []byte("#!/bin/sh\n")},
				Modes:     map[string]os.FileMode{"/home/dev/run.sh": 0o755},
			},
			execErr:   errors.New("chmod boom"),
			wantChown: "chown -R dev:dev /home /home/dev /home/dev/run.sh",
		},
		{
			name:      "directory creation fails",
			cf:        &ConfigFiles{Provisioned: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v\n")}},
			failPath:  "/home/dev/.config/tool",
			wantChown: "chown -R dev:dev /home /home/dev /home/dev/.config",
		},
		{
			name:      "merged config directory creation fails",
			cf:        &ConfigFiles{Merged: []byte("{}"), MergedPath: opencodeMergedPath},
			failPath:  "/home/dev/.config/opencode",
			wantChown: "chown -R dev:dev /home /home/dev /home/dev/.config",
		},
		{
			name: "a later write fails",
			cf: &ConfigFiles{
				HomeFiles:   map[string][]byte{"/home/dev/.gitconfig": []byte("x=y\n")},
				Provisioned: map[string][]byte{"/home/dev/cfg.toml": []byte("k=v\n")},
			},
			failPath:  "/home/dev/cfg.toml",
			wantChown: "chown -R dev:dev /home /home/dev /home/dev/.gitconfig",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			sb := &msbtest.MockSandbox{
				FSValue_:   pathFailingFS{TestFS: msbtest.NewTestFS(nil), failPath: tc.failPath},
				ShellCalls: &calls,
				ExecErr:    tc.execErr,
			}
			if err := Provision(context.Background(), sb, tc.cf); err == nil {
				t.Fatal("Provision error = nil, want the failure")
			}
			if len(calls) != 1 || calls[0] != tc.wantChown {
				t.Errorf("chown calls = %v, want [%q]", calls, tc.wantChown)
			}
		})
	}
}

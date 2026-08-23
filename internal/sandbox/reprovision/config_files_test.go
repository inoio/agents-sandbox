package reprovision

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestLoadConfigFilesPopulatesHooks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	testutil.WritePath(t, filepath.Join(cp.ProjectConfigDir(), "connect.sh"), "#!/bin/sh\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "home.yaml",
		".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n  root: true\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFiles(configpaths.Get().UserOpencodeConfigDir(), &ui)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	want := []homeconfig.HookSpec{
		{
			Target:      "/home/dev/.vpn/connect.sh",
			Source:      filepath.Join(cp.ProjectConfigDir(), "connect.sh"),
			Interpreter: "/bin/sh",
			Root:        true,
		},
	}
	if !reflect.DeepEqual(cf.Hooks, want) {
		t.Errorf("Hooks = %v, want %v", cf.Hooks, want)
	}
}

// TestProvisionChownsHomeFiles verifies that Provision writes the home files and
// the merged opencode config, then chowns every provisioned path to dev:dev so
// the files are readable by the runtime user.
func TestProvisionChownsHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		OpenCode:    []byte(`{"model":"x"}`),
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig":            []byte("user.name=X\n"),
			"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
		},
	}
	fs := msb.NewTestFS(nil, nil)
	var calls []string
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &calls}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	for path := range cf.HomeFiles {
		if _, ok := fs.Writes[path]; !ok {
			t.Errorf("home file %s was not written", path)
		}
	}
	if _, ok := fs.Writes[OpenCodeConfigPath(VMHomeDir)]; !ok {
		t.Error("opencode config was not written")
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 chown shell call, got %d: %v", len(calls), calls)
	}
	wantChown := "chown -R dev:dev /home /home/dev /home/dev/.config /home/dev/.config/opencode " +
		"/home/dev/.config/opencode/opencode.json /home/dev/.config/tool /home/dev/.config/tool/cfg.toml " +
		"/home/dev/.gitconfig"
	if calls[0] != wantChown {
		t.Errorf("shell command = %q, want %q", calls[0], wantChown)
	}
}

// TestProvisionNoFilesSkipsChown verifies that Provision does not run a chown
// when there are no files to provision.
func TestProvisionNoFilesSkipsChown(t *testing.T) {
	var calls []string
	sb := &msb.MockSandbox{FSValue_: msb.NewTestFS(nil, nil), ShellCalls: &calls}
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
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = writeErr
	sb := &msb.MockSandbox{FSValue_: fs, ShellErr: chownErr}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, writeErr) {
		t.Errorf("Provision error does not wrap write error: %v", err)
	}
	if !errors.Is(err, chownErr) {
		t.Errorf("Provision error does not wrap chown error: %v", err)
	}
}

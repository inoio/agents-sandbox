package reprovision

import (
	"bytes"
	"context"
	"encoding/json"
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

const claudeState = "/home/dev/.claude.json"

func workspaceTrust() map[string]any {
	return map[string]any{"projects": map[string]any{"/workspace": map[string]any{"hasTrustDialogAccepted": true}}}
}

func TestMergeStateDefaults(t *testing.T) {
	cases := map[string]struct {
		content     string
		wantChanged bool
		wantOK      bool
	}{
		"a missing state file is created": {"", true, true},
		"other keys and projects are kept": {
			`{"oauthAccount":{"email":"a@b"},"tip":"<a&b>","numStartups":12345678901234567,"projects":{"/other":{"x":1}}}`,
			true,
			true,
		},
		"an accepted trust is left alone": {
			`{"projects":{"/workspace":{"hasTrustDialogAccepted":true,"history":[]}}}`,
			false,
			true,
		},
		"a refused trust is kept": {
			`{"projects":{"/workspace":{"hasTrustDialogAccepted":false}}}`,
			false,
			true,
		},
		"a non-table projects is replaced":    {`{"projects":"x"}`, true, true},
		"an undecodable file is left alone":   {`{not json`, false, false},
		"a non-object document is left alone": {`[1,2]`, false, false},
		"a null document is left alone":       {`null`, false, false},
		"a one-byte document is left alone":   {`7`, false, false},
	}
	for name, tc := range cases {
		merged, changed, ok := mergeStateDefaults([]byte(tc.content), workspaceTrust())
		if changed != tc.wantChanged || ok != tc.wantOK {
			t.Errorf("%s: changed, ok = %v, %v; want %v, %v", name, changed, ok, tc.wantChanged, tc.wantOK)
			continue
		}
		if !changed {
			continue
		}
		var document map[string]any
		decoder := json.NewDecoder(bytes.NewReader(merged))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err != nil {
			t.Fatalf("%s: merged is not JSON: %v", name, err)
		}
		projects := document["projects"].(map[string]any)
		if projects["/workspace"].(map[string]any)["hasTrustDialogAccepted"] != true {
			t.Errorf("%s: merged = %s, want the trust accepted", name, merged)
		}
		if name == "other keys and projects are kept" {
			if !strings.Contains(string(merged), "\n  \"tip\": \"<a&b>\"") {
				t.Errorf("%s: merged = %s, want two-space indent and no HTML escaping", name, merged)
			}
			if document["oauthAccount"] == nil || projects["/other"] == nil ||
				document["numStartups"].(json.Number).String() != "12345678901234567" {
				t.Errorf("%s: merged = %s, want every other key kept exactly", name, merged)
			}
		}
	}
}

func TestProvisionAppliesStateDefaultsOnlyWhenTheyChangeSomething(t *testing.T) {
	cf := &ConfigFiles{StateDefaults: []StateDefault{{VMPath: claudeState, Keys: workspaceTrust()}}}
	fs := msbtest.NewTestFS(map[string][]byte{claudeState: []byte(`{"oauthAccount":{}}`)})
	var shell, exec []string
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &shell, ExecCalls: &exec}

	if err := Provision(context.Background(), sb, cf, testLogger(t)); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	temporary := claudeState + stateTempSuffix
	if fs.Writes[temporary] == nil {
		t.Fatal("the state default was not written")
	}
	if !slices.Equal(exec, []string{"chmod 0600 " + temporary, "mv -f " + temporary + " " + claudeState}) ||
		len(shell) != 1 || !strings.HasSuffix(shell[0], " "+claudeState) {
		t.Errorf(
			"exec = %v, shell = %v; want the state file made private, renamed into place, and chowned",
			exec,
			shell,
		)
	}

	settled := msbtest.NewTestFS(map[string][]byte{claudeState: fs.Writes[temporary]})
	sb = &msbtest.MockSandbox{FSValue_: settled, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, cf, testLogger(t)); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, written := settled.Writes[claudeState]; written {
		t.Error("a state file that already holds the defaults was rewritten")
	}
}

func TestProvisionLeavesAnUndecodableStateFile(t *testing.T) {
	cf := &ConfigFiles{StateDefaults: []StateDefault{{VMPath: claudeState, Keys: workspaceTrust()}}}
	fs := msbtest.NewTestFS(map[string][]byte{claudeState: []byte(`{not json`)})
	if err := Provision(
		context.Background(),
		&msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}},
		cf,
		testLogger(t),
	); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, written := fs.Writes[claudeState]; written {
		t.Error("an undecodable state file was overwritten")
	}
}

func TestLoadConfigFilesCarriesTheAgentsStateDefaults(t *testing.T) {
	a, _ := agent.Lookup("claude-code")
	got := stateDefaultsFor(a, "/home/dev")
	if len(got) != 1 || got[0].VMPath != claudeState {
		t.Errorf("stateDefaultsFor = %+v, want the VM path of .claude.json", got)
	}
	plain, _ := agent.Lookup("codex")
	if got := stateDefaultsFor(plain, "/home/dev"); len(got) != 0 {
		t.Errorf("stateDefaultsFor(codex) = %+v, want none", got)
	}
}

func TestMaterializeAppliesStateDefaults(t *testing.T) {
	target := t.TempDir()
	cf := &ConfigFiles{StateDefaults: []StateDefault{{VMPath: claudeState, Keys: workspaceTrust()}}}

	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	info, err := os.Stat(filepath.Join(target, ".claude.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state file = %v, %v; want it written private", info, err)
	}
}

func TestSetKeysSetsEveryKey(t *testing.T) {
	document := map[string]any{"a": 1}
	if !setKeys(document, map[string]any{"a": 9, "b": 2, "c": 3}) || document["a"] != 1 || document["b"] != 2 ||
		document["c"] != 3 {
		t.Errorf("document = %v, want every missing key set and the present one kept", document)
	}
}

func TestProvisionAppliesEveryStateDefault(t *testing.T) {
	other := "/home/dev/.other.json"
	cf := &ConfigFiles{StateDefaults: []StateDefault{
		{VMPath: claudeState, Keys: workspaceTrust()},
		{VMPath: other, Keys: map[string]any{"x": true}},
	}}
	fs := msbtest.NewTestFS(
		map[string][]byte{claudeState: []byte(`{"projects":{"/workspace":{"hasTrustDialogAccepted":true}}}`)},
	)
	var shell []string
	if err := Provision(
		context.Background(),
		&msbtest.MockSandbox{FSValue_: fs, ShellCalls: &shell},
		cf,
		testLogger(t),
	); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if fs.Writes[other+stateTempSuffix] == nil {
		t.Error("a later state default was skipped after a settled one")
	}
	if len(shell) != 1 || shell[0] != "chown -R dev:dev /home /home/dev /home/dev/.other.json" {
		t.Errorf("shell = %v, want the created dirs and the state file chowned", shell)
	}
}

func TestProvisionReportsStateDefaultFailures(t *testing.T) {
	cf := &ConfigFiles{StateDefaults: []StateDefault{{VMPath: claudeState, Keys: workspaceTrust()}}}
	writeErr, chmodErr := errors.New("write boom"), errors.New("chmod boom")
	writeFailing := msbtest.NewTestFS(nil)
	writeFailing.WriteErr = writeErr
	cases := map[string]struct {
		fs        msb.SandboxFS
		execErr   error
		wantErr   error
		wantChown string
	}{
		"mkdir": {
			pathFailingFS{TestFS: msbtest.NewTestFS(nil), failPath: "/home/dev"},
			nil,
			nil,
			"chown -R dev:dev /home",
		},
		"write": {writeFailing, nil, writeErr, "chown -R dev:dev /home /home/dev"},
		"chmod": {msbtest.NewTestFS(nil), chmodErr, chmodErr, "chown -R dev:dev /home /home/dev"},
	}
	for name, tc := range cases {
		var shell []string
		sb := &msbtest.MockSandbox{FSValue_: tc.fs, ShellCalls: &shell, ExecErr: tc.execErr}
		err := Provision(context.Background(), sb, cf, testLogger(t))
		if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
			t.Errorf("%s: Provision = %v, want the wrapped failure", name, err)
		}
		if len(shell) != 1 || shell[0] != tc.wantChown {
			t.Errorf("%s: shell = %v, want %q for what was made before the failure", name, shell, tc.wantChown)
		}
	}
}

func TestMaterializeLeavesASettledStateFile(t *testing.T) {
	target := t.TempDir()
	settled := `{"projects":{"/workspace":{"hasTrustDialogAccepted":true}}}`
	if err := os.WriteFile(filepath.Join(target, ".claude.json"), []byte(settled), 0o644); err != nil {
		t.Fatal(err)
	}
	cf := &ConfigFiles{StateDefaults: []StateDefault{
		{VMPath: claudeState, Keys: workspaceTrust()},
		{VMPath: "/home/dev/.other.json", Keys: map[string]any{"x": true}},
	}}

	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	if content, _ := os.ReadFile(filepath.Join(target, ".claude.json")); string(content) != settled {
		t.Errorf("state file = %s, want it untouched", content)
	}
	if _, err := os.Stat(filepath.Join(target, ".other.json")); err != nil {
		t.Errorf("a later state default was skipped after a settled one: %v", err)
	}
}

func TestMaterializeReportsStateDefaultFailures(t *testing.T) {
	cases := map[string]struct {
		vmPath, target string
	}{
		"outside the home": {"/etc/state.json", t.TempDir()},
	}
	for name, tc := range cases {
		cf := &ConfigFiles{StateDefaults: []StateDefault{{VMPath: tc.vmPath, Keys: workspaceTrust()}}}
		if err := Materialize(cf, VMHomeDir, tc.target); err == nil || errors.Unwrap(err) == nil {
			t.Errorf("%s: Materialize = %v, want the wrapped failure", name, err)
		}
	}
}

// failingStateFS fails Exists or Read for claudeState only.
type failingStateFS struct {
	*msbtest.TestFS

	existsErr, readErr error
}

func (f failingStateFS) Exists(ctx context.Context, path string) (bool, error) {
	if f.existsErr != nil && path == claudeState {
		return false, f.existsErr
	}
	return f.TestFS.Exists(ctx, path)
}

func (f failingStateFS) Read(ctx context.Context, path string) ([]byte, error) {
	if f.readErr != nil && path == claudeState {
		return nil, f.readErr
	}
	return f.TestFS.Read(ctx, path)
}

// renameFailingSandbox fails the rename's exec with an error.
type renameFailingSandbox struct{ *msbtest.MockSandbox }

func (s renameFailingSandbox) Exec(
	ctx context.Context,
	cmd string,
	args []string,
	req msb.ExecRequest,
) (msb.ShellResult, error) {
	if cmd == "mv" {
		return nil, errors.New("exec boom")
	}
	return s.MockSandbox.Exec(ctx, cmd, args, req)
}

// TestProvisionNeverReplacesAStateFileItCannotRead: a state file holds the
// agent's login and history, so a failed read must not look like a missing file.
func TestProvisionNeverReplacesAStateFileItCannotRead(t *testing.T) {
	other := "/home/dev/.other.json"
	cf := &ConfigFiles{StateDefaults: []StateDefault{
		{VMPath: claudeState, Keys: workspaceTrust()},
		{VMPath: other, Keys: map[string]any{"x": true}},
	}}
	for name, fs := range map[string]failingStateFS{
		"read":   {TestFS: msbtest.NewTestFS(map[string][]byte{claudeState: []byte(`{"oauthAccount":{}}`)}), readErr: errors.New("stream boom")},
		"exists": {TestFS: msbtest.NewTestFS(nil), existsErr: errors.New("exists boom")},
	} {
		if err := Provision(
			context.Background(),
			&msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}},
			cf,
			testLogger(t),
		); err != nil {
			t.Fatalf("%s: Provision: %v", name, err)
		}
		if _, written := fs.Writes[claudeState+stateTempSuffix]; written {
			t.Errorf("%s: writes = %v, want the unreadable state file left alone", name, fs.Writes)
		}
		if fs.Writes[other+stateTempSuffix] == nil {
			t.Errorf("%s: a later state default was skipped after an unreadable one", name)
		}
	}
}

func TestProvisionRemovesTheTemporaryStateFileOnAFailedRename(t *testing.T) {
	cf := &ConfigFiles{StateDefaults: []StateDefault{{VMPath: claudeState, Keys: workspaceTrust()}}}
	fs := msbtest.NewTestFS(nil)
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}, ExecOut: map[string]msb.ShellResult{
		"mv -f " + claudeState + stateTempSuffix + " " + claudeState: msbtest.NewTestResult(
			false,
			1,
			"",
			"mv failed",
			nil,
		),
	}}
	err := Provision(context.Background(), sb, cf, testLogger(t))
	if err == nil || !strings.Contains(err.Error(), "mv failed") {
		t.Errorf("Provision = %v, want the rename failure", err)
	}
	if !slices.Contains(fs.Removed, claudeState+stateTempSuffix) {
		t.Errorf("removed = %v, want the temporary file cleaned up", fs.Removed)
	}
}

func TestMaterializeNeverReplacesAStateFileItCannotRead(t *testing.T) {
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(target, ".claude.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	cf := &ConfigFiles{StateDefaults: []StateDefault{
		{VMPath: claudeState, Keys: workspaceTrust()},
		{VMPath: "/home/dev/.other.json", Keys: map[string]any{"x": true}},
	}}
	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Errorf("Materialize = %v, want the unreadable state file skipped", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".other.json")); err != nil {
		t.Errorf("a later state default was skipped after an unreadable one: %v", err)
	}
}

func TestMaterializeMergesIntoAnExistingStateFile(t *testing.T) {
	target := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(target, ".claude.json"),
		[]byte(`{"oauthAccount":{"email":"a@b"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	cf := &ConfigFiles{StateDefaults: []StateDefault{{VMPath: claudeState, Keys: workspaceTrust()}}}
	if err := Materialize(cf, VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	content, _ := os.ReadFile(filepath.Join(target, ".claude.json"))
	if !strings.Contains(string(content), "a@b") || !strings.Contains(string(content), "hasTrustDialogAccepted") {
		t.Errorf("state file = %s, want the trust merged beside the account", content)
	}
}

func TestProvisionReportsRenameFailures(t *testing.T) {
	cf := &ConfigFiles{StateDefaults: []StateDefault{{VMPath: claudeState, Keys: workspaceTrust()}}}
	chmodFailing := &msbtest.MockSandbox{
		FSValue_:   msbtest.NewTestFS(nil),
		ShellCalls: &[]string{},
		ExecOut: map[string]msb.ShellResult{
			"chmod 0600 " + claudeState + stateTempSuffix: msbtest.NewTestResult(false, 1, "", "chmod failed", nil),
		},
	}
	execFailing := renameFailingSandbox{&msbtest.MockSandbox{FSValue_: msbtest.NewTestFS(nil), ShellCalls: &[]string{}}}
	for name, sb := range map[string]msb.SandboxExecutor{"chmod": chmodFailing, "mv exec": execFailing} {
		if err := Provision(context.Background(), sb, cf, testLogger(t)); err == nil {
			t.Errorf("%s: a failed rename was not reported", name)
		}
	}
}

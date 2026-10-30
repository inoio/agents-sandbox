package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestOpencodeAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	cmd, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("opencode should implement AttachRunner")
	}
	got := cmd.AttachCommand("/workspace", []string{"--print-logs"})
	want := "opencode attach http://127.0.0.1:4096 --dir '/workspace' '--print-logs'"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestOpencodeConfigDirAliases(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsConfigDirAliaser(a); ok {
		t.Error("opencode (v1) must not declare config dir aliases")
	}
}

func TestOpencodeAttachCommandQuotesTarget(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	cmd, _ := agent.AsAttachRunner(a)
	cases := map[string]string{
		"/workspace/my notes": "opencode attach http://127.0.0.1:4096 --dir '/workspace/my notes'",
		"/workspace/it's":     `opencode attach http://127.0.0.1:4096 --dir '/workspace/it'\''s'`,
	}
	for target, want := range cases {
		if got := cmd.AttachCommand(target, nil); got != want {
			t.Errorf("AttachCommand(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestOpencodeWorktreeCreateCmdUsesName(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature/x"}, agent.WorktreeProject{})
	want := "curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"feature/x\"}'"
	if got != want {
		t.Errorf("WorktreeCreateCmd = %q, want %q", got, want)
	}
}

func TestOpencodeDaemonCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	local := daemon.DaemonCommand(false)
	if local.Binary != "opencode" || !slices.Contains(local.Args, "serve") {
		t.Errorf("local DaemonCommand = %+v, want opencode serve", local)
	}
	if !slices.Contains(local.Args, "--hostname") || !slices.Contains(local.Args, "127.0.0.1") {
		t.Errorf("local DaemonCommand = %+v, want --hostname 127.0.0.1", local)
	}
	serve := daemon.DaemonCommand(true)
	if !slices.Contains(serve.Args, "0.0.0.0") {
		t.Errorf("serve DaemonCommand = %+v, want hostname 0.0.0.0", serve)
	}
}

func TestOpencodeDaemonHealthCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	if !strings.Contains(daemon.DaemonHealthCmd(), "127.0.0.1:4096") {
		t.Errorf("DaemonHealthCmd = %q, want port 4096", daemon.DaemonHealthCmd())
	}
	if hint := daemon.DaemonAuthHint(); !strings.Contains(hint, "OPENCODE_SERVER_PASSWORD") {
		t.Errorf("DaemonAuthHint = %q, want mention of OPENCODE_SERVER_PASSWORD", hint)
	}
}

func TestOpencodeDaemonHealthParse(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	ok, err := daemon.DaemonHealthParse(`{"healthy":true,"version":"1.0.0"}`)
	if err != nil || !ok {
		t.Errorf("DaemonHealthParse(healthy) = %v, %v, want true, nil", ok, err)
	}
	notOK, err := daemon.DaemonHealthParse(`{"healthy":false}`)
	if err != nil || notOK {
		t.Errorf("DaemonHealthParse(unhealthy) = %v, %v, want false, nil", notOK, err)
	}
	parsed, err := daemon.DaemonHealthParse("not json")
	if parsed {
		t.Error("DaemonHealthParse(invalid) ok = true, want false")
	}
	if _, ok := errors.AsType[*json.SyntaxError](err); !ok {
		t.Errorf("DaemonHealthParse(invalid) error = %v, want *json.SyntaxError", err)
	}
}

func TestOpencodeWorktreeHasNoResolveStep(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	if got := worktree.WorktreeResolveCmd("/workspace"); got != "" {
		t.Errorf("WorktreeResolveCmd = %q, want empty (v1 routes are global)", got)
	}
	if got := worktree.WorktreeListCmd(
		agent.WorktreeProject{},
	); got != "curl -sf http://127.0.0.1:4096/experimental/worktree" {
		t.Errorf("WorktreeListCmd = %q, want the global v1 route", got)
	}
	// v1 has no resolve step, so the paired parser is never called in
	// production; it must still report no project rather than fabricate one.
	if _, ok := worktree.WorktreeParseProject(`{"project":{"id":"abc","canonical":"/repo"}}`); ok {
		t.Error("WorktreeParseProject must report no project for v1")
	}
}

// TestOpencodeWorktreeParseList pins the v1 list parser: path strings and
// {directory} objects are accepted, but a non-array payload or an entry without
// a non-empty string directory must not read as "no worktrees".
func TestOpencodeWorktreeParseList(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	cases := []struct {
		name string
		in   string
		want []string
		ok   bool
	}{
		{name: "string paths", in: `["/wt/a","/wt/b"]`, want: []string{"/wt/a", "/wt/b"}, ok: true},
		{name: "objects", in: `[{"directory":"/wt/a"}]`, want: []string{"/wt/a"}, ok: true},
		{name: "empty array", in: `[]`, want: []string{}, ok: true},
		{name: "non-array object", in: `{"directory":"/wt/a"}`, ok: false},
		{name: "null", in: `null`, ok: false},
		{name: "empty input", in: ``, ok: false},
		{name: "invalid json", in: `not json`, ok: false},
		{name: "renamed field", in: `[{"path":"/wt/a"}]`, ok: false},
		{name: "blank directory", in: `[{"directory":""}]`, ok: false},
		{name: "numeric entry", in: `[123]`, ok: false},
		{name: "numeric directory", in: `[{"directory":123}]`, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := worktree.WorktreeParseList(tc.in, agent.WorktreeProject{})
			if ok != tc.ok || !slices.Equal(got, tc.want) {
				t.Errorf("WorktreeParseList(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestOpencodeWorktreeCreateCmdWithBase(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "f", Base: "main"}, agent.WorktreeProject{})
	if !strings.Contains(got, `"startCommand":"git reset --hard main"`) {
		t.Errorf("WorktreeCreateCmd(with base) = %q", got)
	}
}

// TestOpencodeWorktreeNotDetached pins that v1 keeps its worktrees on a branch,
// so the launcher never reports a detached HEAD for opencode.
func TestOpencodeWorktreeNotDetached(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsDetachedWorktreeReporter(a); ok {
		t.Error("opencode must not implement DetachedWorktreeReporter (v1 keeps a branch)")
	}
}

func TestOpencodeWorktreeParseDir(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	dir, ok := worktree.WorktreeParseDir(`{"directory":"/home/dev/ws"}`)
	if !ok || dir != "/home/dev/ws" {
		t.Errorf("WorktreeParseDir = %q, %v, want /home/dev/ws, true", dir, ok)
	}
	if _, ok := worktree.WorktreeParseDir("not json"); ok {
		t.Error("WorktreeParseDir(invalid) should return not-ok")
	}
	if _, ok := worktree.WorktreeParseDir(`{"directory":""}`); ok {
		t.Error("WorktreeParseDir(empty) should return not-ok")
	}
	// A decode error that nonetheless accepted a directory value (a duplicate key)
	// must not be mistaken for a successful parse.
	if dir, ok := worktree.WorktreeParseDir(`{"directory":"/x","directory":123}`); ok {
		t.Errorf("WorktreeParseDir(duplicate key) = %q, %v, want not-ok", dir, ok)
	}
}

func TestOpencodeLatestVersionCancelledCtx(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("opencode should implement UpgradeChecker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestOpencodeNewerThan(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	checker := mustChecker(t, a)
	gt, err := checker.NewerThan("1.2.0", "1.1.0")
	if err != nil || !gt {
		t.Errorf("NewerThan(1.2.0, 1.1.0) = %v, %v, want true, nil", gt, err)
	}
	lt, err := checker.NewerThan("1.0.0", "1.1.0")
	if err != nil || lt {
		t.Errorf("NewerThan(1.0.0, 1.1.0) = %v, %v, want false, nil", lt, err)
	}
	if _, err := checker.NewerThan("notaversion", "1.0.0"); err == nil {
		t.Error("NewerThan(invalid) should error")
	}
}

func TestOpencodeConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "opencode*.json*" {
		t.Errorf("SnippetPattern = %q", merger.SnippetPattern())
	}
	if merger.VMConfigPath("/home/user") != filepath.Join("/home/user", ".config", "opencode", "opencode.jsonc") {
		t.Errorf("VMConfigPath = %q", merger.VMConfigPath("/home/user"))
	}
}

func TestOpencodeConfigFileNames(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode should implement ConfigMerger")
	}
	names := merger.ConfigFileNames()
	want := []string{
		"config.json",
		"opencode.json",
		"opencode.jsonc",
		"opencode.json5",
		"opencode.yaml",
		"opencode.yml",
	}
	if !slices.Equal(names, want) {
		t.Errorf("ConfigFileNames = %v, want %v", names, want)
	}
}

func TestOpencodeProvisionRules(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("opencode should implement Provisioner")
	}
	rules := provisioner.ProvisionRules()
	if len(rules) == 0 {
		t.Fatal("ProvisionRules must not be empty")
	}
	found := false
	for _, r := range rules {
		if r.Dir == ".config/opencode" {
			found = true
		}
	}
	if !found {
		t.Errorf("ProvisionRules missing .config/opencode: %+v", rules)
	}
}

func TestOpencodeMigrationSpec(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	provider, ok := agent.AsMigrationSpecProvider(a)
	if !ok {
		t.Fatal("opencode should implement MigrationSpecProvider")
	}
	spec := provider.MigrationSpec()
	if spec.ManagedCredential != "auth.json" || spec.CredentialTarget != ".local/share/opencode/auth.json" {
		t.Errorf("unexpected migration paths: %+v", spec)
	}
	if len(spec.KnownProviderHosts) == 0 {
		t.Fatal("expected known provider hosts")
	}
}

func mustDaemon(t *testing.T, a agent.Agent) agent.DaemonProvider {
	t.Helper()
	daemon, ok := agent.AsDaemonProvider(a)
	if !ok {
		t.Fatal("opencode should implement DaemonProvider")
	}
	return daemon
}

func mustWorktree(t *testing.T, a agent.Agent) agent.WorktreeProvider {
	t.Helper()
	worktree, ok := agent.AsWorktreeProvider(a)
	if !ok {
		t.Fatal("opencode should implement WorktreeProvider")
	}
	return worktree
}

func mustChecker(t *testing.T, a agent.Agent) agent.UpgradeChecker {
	t.Helper()
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("opencode should implement UpgradeChecker")
	}
	return checker
}

func TestOpencodeEventStream(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	provider, ok := agent.AsEventStreamProvider(a)
	if !ok {
		t.Fatal("opencode should implement EventStreamProvider")
	}
	got := provider.EventStream()
	if got.Parse == nil {
		t.Fatal("EventStream().Parse is nil")
	}
	got.Parse = nil
	if got.SessionList == nil {
		t.Fatal("EventStream().SessionList is nil")
	}
	if want := "curl -sf -m 30 http://127.0.0.1:4096/session"; got.SessionList.Command() != want {
		t.Errorf("SessionList.Command() = %q, want %q", got.SessionList.Command(), want)
	}
	if children := got.SessionList.Parse([]byte(
		`[{"id":"ses_child","parentID":"ses_top"}]`,
	)); len(children) != 1 || children[0] != "ses_child" {
		t.Errorf("SessionList.Parse() = %v, want [ses_child]", children)
	}
	got.SessionList = nil
	want := agent.EventStreamSpec{
		StreamCommand:     "curl -N -s http://127.0.0.1:4096/global/event",
		BusyEvents:        []string{"message.part.updated", "session.updated"},
		AwaitingInput:     []string{"permission.asked", "question.asked"},
		IdleEvents:        []string{"session.idle"},
		ErrorEvents:       []string{"session.error"},
		StickyErrorEvents: []string{"session.error"},
		SessionEvents:     []string{"session.created", "session.updated"},
		Name:              "opencode",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EventStream() = %+v, want %+v", got, want)
	}
}

func TestOpencodeImplementsSessionStatusProvider(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	p, ok := agent.AsSessionStatusProvider(a)
	if !ok {
		t.Fatal("opencode should implement SessionStatusProvider")
	}
	if got, want := p.SessionStatusCmd(), "curl -sf http://127.0.0.1:4096/session/status"; got != want {
		t.Errorf("SessionStatusCmd() = %q, want %q", got, want)
	}
	if got, want := p.QuestionListCmd(), "curl -sf http://127.0.0.1:4096/question"; got != want {
		t.Errorf("QuestionListCmd() = %q, want %q", got, want)
	}
}

func TestPiLacksSessionStatusProvider(t *testing.T) {
	a, _ := agent.Lookup("pi")
	if _, ok := agent.AsSessionStatusProvider(a); ok {
		t.Error("pi should not implement SessionStatusProvider")
	}
}

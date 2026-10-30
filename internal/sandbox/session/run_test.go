package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestBuildAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(a, "/workspace", []string{"foo"})
	if !strings.Contains(got, "opencode attach") {
		t.Errorf("expected 'opencode attach' in command, got %q", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:4096") {
		t.Errorf("expected daemon URL in command, got %q", got)
	}
	if !strings.Contains(got, "--dir '/workspace'") {
		t.Errorf("expected --dir '/workspace' in command, got %q", got)
	}
	if strings.Contains(got, "--auto") {
		t.Errorf("did not expect --auto flag (removed by human), got %q", got)
	}
	if !strings.Contains(got, "foo") {
		t.Errorf("expected forwarded args in command, got %q", got)
	}
}

func TestBuildAttachCommandWorktreeTarget(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(a, "/home/dev/.local/share/opencode/worktree/abc/feat", nil)
	if !strings.Contains(got, "--dir '/home/dev/.local/share/opencode/worktree/abc/feat'") {
		t.Errorf("expected worktree dir in command, got %q", got)
	}
}

func TestBuildAttachCommandQuotesTargetWithSpaces(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(a, "/workspace/my notes", nil)
	if !strings.Contains(got, "--dir '/workspace/my notes'") {
		t.Errorf("expected the quoted target to keep the space, got %q", got)
	}
}

func TestBuildAttachCommandOpencode(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(a, "/workspace/x", nil)
	want := "opencode attach http://127.0.0.1:4096 --dir '/workspace/x'"
	if got != want {
		t.Errorf("attach = %q, want %q", got, want)
	}
}

// nonAttachAgent implements agent.Agent but not agent.AttachRunner, so
// buildAttachCommand must return "" for it.
type nonAttachAgent struct{}

func (nonAttachAgent) Name() string               { return "nonattach" }
func (nonAttachAgent) ConfigDirName() string      { return "nonattach" }
func (nonAttachAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

func TestBuildAttachCommandNoAttachRunner(t *testing.T) {
	got := buildAttachCommand(nonAttachAgent{}, "/workspace/x", nil)
	if got != "" {
		t.Errorf("attach = %q, want empty string for non-AttachRunner agent", got)
	}
}

func TestServeOnlyMessage(t *testing.T) {
	opencode, _ := agent.Lookup("opencode")
	msg := serveOnlyMessage(opencode, "127.0.0.1", "4096")
	for _, want := range []string{"Connect a client to", "http://127.0.0.1:4096", "OPENCODE_SERVER_PASSWORD", "Ctrl-D"} {
		if !strings.Contains(msg, want) {
			t.Errorf("serveOnlyMessage missing %q in:\n%s", want, msg)
		}
	}
}

// TestServeOnlyMessageOpencode2StatesCredential verifies the serve-only guidance
// tells an external client how to authenticate to opencode2: its basic auth is
// mandatory with a baked credential, so the old opt-in/env wording would be
// wrong and would leave the client unable to connect.
func TestServeOnlyMessageOpencode2StatesCredential(t *testing.T) {
	opencode2, _ := agent.Lookup("opencode2")
	msg := serveOnlyMessage(opencode2, "127.0.0.1", "4096")
	for _, want := range []string{"http://127.0.0.1:4096", `user "opencode"`, `password "opencode"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("serveOnlyMessage(opencode2) missing %q in:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Optional:") {
		t.Errorf("serveOnlyMessage(opencode2) must not offer optional auth:\n%s", msg)
	}
}

func TestRunServeOnlyBlocksUntilCancel(t *testing.T) {
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	ui := &termio.Mock{}
	go func() { got <- runServeOnly(ctx, opencodeAgent(t), sb, ui, 4096) }()
	cancel()
	select {
	case err := <-got:
		if err == nil {
			t.Error("expected ctx.Err from completed runServeOnly")
		}
	case <-time.After(2 * time.Second):
		t.Error("runServeOnly did not return after ctx cancel")
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected Infof call to print connect URL, got no info calls")
	}
	found := false
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, "http://127.0.0.1:4096") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected connect URL 'http://127.0.0.1:4096' in info output, got %v", ui.InfoCalls)
	}
}

func TestRunAttachUsesAttachWithForRoot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	release, err := state.AcquireClientLease(state.Key{Slug: "roottest", Agent: "opencode"})
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	err = runAttach(context.Background(), sb, "roottest", "/workspace/sub", ui,
		options.RunOptions{Root: true, ReapPolicy: options.NewReapPolicy(false, 5)}, "-l")
	if err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if sb.AttachUser != "root" {
		t.Errorf("AttachUser = %q, want %q", sb.AttachUser, "root")
	}
	if sb.AttachCwd != "/workspace/sub" {
		t.Errorf("AttachCwd = %q, want %q", sb.AttachCwd, "/workspace/sub")
	}
}

func TestRunAttachDefaultDoesNotUseRoot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	release, err := state.AcquireClientLease(state.Key{Slug: "defaulttest", Agent: "opencode"})
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	err = runAttach(context.Background(), sb, "defaulttest", "/workspace/sub", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}, "-l")
	if err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if sb.AttachUser != "" {
		t.Errorf("AttachUser = %q, want empty for default (non-root) attach", sb.AttachUser)
	}
	if sb.AttachCwd != "/workspace/sub" {
		t.Errorf("AttachCwd = %q, want %q", sb.AttachCwd, "/workspace/sub")
	}
}

type fakePrepared struct {
	sb            msb.Sandbox
	target        string
	serveHostPort int
	guestCWD      string
	key           state.Key
	homeVolume    string
}

func (f *fakePrepared) Cleanup()             {}
func (f *fakePrepared) Sandbox() msb.Sandbox { return f.sb }
func (f *fakePrepared) Target() string       { return f.target }
func (f *fakePrepared) ServeHostPort() int   { return f.serveHostPort }
func (f *fakePrepared) GuestCWD() string     { return f.guestCWD }
func (f *fakePrepared) Key() state.Key       { return f.key }
func (f *fakePrepared) HomeVolume() string   { return f.homeVolume }

// --- Run: dry-run ---

// stubPreviewSandbox swaps the preview seam for a fixed preview and restores it
// on cleanup. Tests that run with options.ModePreview must use it so the real
// PreviewSandbox never touches host state.
func stubPreviewSandbox(t *testing.T, preview sandbox.Preview) {
	t.Helper()
	orig := previewSandbox
	previewSandbox = func(context.Context, options.RunOptions, termio.UI) (sandbox.Preview, error) {
		return preview, nil
	}
	t.Cleanup(func() { previewSandbox = orig })
}

func assertInfoCalls(t *testing.T, ui *termio.Mock, want []string) {
	t.Helper()
	if len(ui.InfoCalls) != len(want) {
		t.Fatalf("info calls = %v, want %v", ui.InfoCalls, want)
	}
	for i, line := range want {
		if ui.InfoCalls[i] != line {
			t.Errorf("info call %d = %q, want %q", i, ui.InfoCalls[i], line)
		}
	}
}

// hasDryRunMessage reports whether any info line is the skip-VM dry-run notice
// that Run and Shell print only for a skip-VM mode.
func hasDryRunMessage(infoCalls []string) bool {
	for _, line := range infoCalls {
		if strings.HasPrefix(line, "dry-run: Would start") {
			return true
		}
	}
	return false
}

func TestRunDryRunRendersPreview(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	stubPreviewSandbox(t, sandbox.Preview{
		AgentVersion: "1.0.0",
		ImageTag:     "agents-sandbox-runner-x",
		BuildImage:   true,
		BuildReasons: []string{"runner image not present locally"},
		CreateHome:   true,
	})

	err := Run(context.Background(), options.RunOptions{}, options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.0.0",
		"dry-run: would build runner image agents-sandbox-runner-x (runner image not present locally)",
		"dry-run: would create home volume",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunRendersReusePreview(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	stubPreviewSandbox(t, sandbox.Preview{
		AgentVersion: "1.2.3",
		ImageTag:     "agents-sandbox-runner-y",
		HomeVolume:   "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), options.RunOptions{}, options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.2.3",
		"dry-run: would reuse runner image agents-sandbox-runner-y",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunRendersUpgradeCheckSkippedSuffix(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	stubPreviewSandbox(t, sandbox.Preview{
		AgentVersion:        "1.0.0",
		UpgradeCheckSkipped: true,
		ImageTag:            "agents-sandbox-runner-y",
		HomeVolume:          "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), options.RunOptions{}, options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.0.0 (upgrade check not run)",
		"dry-run: would reuse runner image agents-sandbox-runner-y",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunResolveAtBuildMessage(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	stubPreviewSandbox(t, sandbox.Preview{
		ResolveVersionAtBuild: true,
		ImageTag:              "agents-sandbox-runner-z",
		HomeVolume:            "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), options.RunOptions{}, options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version resolved at build time",
		"dry-run: would reuse runner image agents-sandbox-runner-z",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunJoinsMultipleBuildReasons(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	stubPreviewSandbox(t, sandbox.Preview{
		AgentVersion: "1.0.0",
		ImageTag:     "agents-sandbox-runner-x",
		BuildImage:   true,
		BuildReasons: []string{"runner image not present locally", "agent version changed"},
		HomeVolume:   "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), options.RunOptions{}, options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.0.0",
		"dry-run: would build runner image agents-sandbox-runner-x (runner image not present locally; agent version changed)",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunDoesNotPrepareSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	stubPreviewSandbox(t, sandbox.Preview{AgentVersion: "1.0.0", ImageTag: "tag", HomeVolume: "vol"})
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		t.Error("prepareSandbox must not run in dry-run mode")
		return &fakePrepared{}, nil
	}
	t.Cleanup(func() { prepareSandbox = orig })

	if err := Run(context.Background(), options.RunOptions{}, options.ModePreview, ui); err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
}

func TestRunDryRunPropagatesPreviewError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := previewSandbox
	previewSandbox = func(context.Context, options.RunOptions, termio.UI) (sandbox.Preview, error) {
		return sandbox.Preview{}, errors.New("preview boom")
	}
	t.Cleanup(func() { previewSandbox = orig })

	err := Run(context.Background(), options.RunOptions{}, options.ModePreview, ui)
	if err == nil || !strings.Contains(err.Error(), "preview boom") {
		t.Errorf("err = %v, want preview error", err)
	}
}

func TestRunDryRunVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	var gotSkipVM bool
	orig := prepareSandbox
	prepareSandbox = func(_ context.Context, _ options.RunOptions, skipVM bool, _ termio.UI) (preparedSandbox, error) {
		gotSkipVM = skipVM
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), options.RunOptions{}, options.ModeSkipVM, ui)
	if err != nil {
		t.Fatalf("Run dry-run-vm: %v", err)
	}
	if !gotSkipVM {
		t.Error("prepareSandbox got skipVM=false under --dry-run-vm, want true")
	}
	if len(ui.InfoCalls) == 0 || ui.InfoCalls[0] != "dry-run: Would start agent session in VM" {
		t.Errorf("info = %v, want dry-run-vm message", ui.InfoCalls)
	}
}

func TestRunServeOnlyPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{})}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}, options.ModeRun, ui)
	}()
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run serve-only did not return after cancel")
	}
	if err == nil {
		t.Fatal("expected ExitError from serve-only path")
	}
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 0 {
		t.Errorf("err = %v, want ExitError code 0", err)
	}
}

func TestRunServeOnlyUsesSessionServeHostPort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{
			sb:            msb.NewMockSandbox(msb.SandboxOpts{}),
			serveHostPort: 4099,
		}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}, options.ModeRun, ui)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run serve-only did not return after cancel")
	}
	found := false
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, "http://127.0.0.1:4099") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected connect URL with session port 4099 in info output, got %v", ui.InfoCalls)
	}
}

func TestRunPrepareSandboxError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return nil, errors.New("prepare boom")
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), options.RunOptions{}, options.ModeRun, ui)
	if err == nil || !strings.Contains(err.Error(), "prepare boom") {
		t.Errorf("err = %v, want prepareSandbox error", err)
	}
}

func TestRunServeOnlyReapFailureWarns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellErr = errors.New("shell failed")
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, options.RunOptions{
			Agent:      "opencode",
			ServeOnly:  true,
			ReapPolicy: options.NewReapPolicy(false, 5),
		}, options.ModeRun, ui)
	}()
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run serve-only did not return after cancel")
	}
	if err == nil {
		t.Fatal("expected ExitError from serve-only path")
	}
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 0 {
		t.Errorf("err = %v, want ExitError code 0", err)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "reap failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reap warning, got %v", ui.WarnCalls)
	}
}

func TestShellPrepareSandboxError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return nil, errors.New("prepare boom")
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(context.Background(), options.RunOptions{}, options.ModeRun, ui)
	if err == nil || !strings.Contains(err.Error(), "prepare boom") {
		t.Errorf("err = %v, want prepareSandbox error", err)
	}
}

func TestRunNormalCallsRunAttach(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
			true, 0, `{"s1":{"type":"idle","attempt":0}}`, "", nil,
		),
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}
	orig := prepareSandbox
	var gotSkipVM bool
	prepareSandbox = func(_ context.Context, _ options.RunOptions, skipVM bool, _ termio.UI) (preparedSandbox, error) {
		gotSkipVM = skipVM
		return &fakePrepared{sb: sb, target: "/workspace"}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), options.RunOptions{
		Agent:      "opencode",
		ReapPolicy: options.NewReapPolicy(true, 5),
	}, options.ModeRun, ui)
	if err != nil {
		t.Fatalf("Run normal: %v", err)
	}
	if gotSkipVM {
		t.Error("prepareSandbox got skipVM=true on a real run, want false")
	}
	if hasDryRunMessage(ui.InfoCalls) {
		t.Errorf("a real run must not take the dry-run-vm path, got info calls %v", ui.InfoCalls)
	}
	found := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "opencode attach") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected attach command in verbose output, got %v", ui.VerboseCalls)
	}
}

// --- Shell ---

func TestShellDryRunRendersPreview(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	stubPreviewSandbox(t, sandbox.Preview{
		AgentVersion: "1.0.0",
		ImageTag:     "agents-sandbox-runner-x",
		BuildImage:   true,
		BuildReasons: []string{"runner image not present locally"},
		HomeVolume:   "agents-sandbox-home-abc",
	})

	err := Shell(context.Background(), options.RunOptions{}, options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Shell dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.0.0",
		"dry-run: would build runner image agents-sandbox-runner-x (runner image not present locally)",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start interactive shell session",
	})
}

func TestShellDryRunDoesNotPrepareSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	stubPreviewSandbox(t, sandbox.Preview{AgentVersion: "1.0.0", ImageTag: "tag", HomeVolume: "vol"})
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		t.Error("prepareSandbox must not run in dry-run mode")
		return &fakePrepared{}, nil
	}
	t.Cleanup(func() { prepareSandbox = orig })

	if err := Shell(context.Background(), options.RunOptions{}, options.ModePreview, ui); err != nil {
		t.Fatalf("Shell dry-run: %v", err)
	}
}

func TestShellDryRunPropagatesPreviewError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := previewSandbox
	previewSandbox = func(context.Context, options.RunOptions, termio.UI) (sandbox.Preview, error) {
		return sandbox.Preview{}, errors.New("preview boom")
	}
	t.Cleanup(func() { previewSandbox = orig })

	err := Shell(context.Background(), options.RunOptions{}, options.ModePreview, ui)
	if err == nil || !strings.Contains(err.Error(), "preview boom") {
		t.Errorf("err = %v, want preview error", err)
	}
}

func TestShellDryRunVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	var gotSkipVM bool
	orig := prepareSandbox
	prepareSandbox = func(_ context.Context, _ options.RunOptions, skipVM bool, _ termio.UI) (preparedSandbox, error) {
		gotSkipVM = skipVM
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(context.Background(), options.RunOptions{}, options.ModeSkipVM, ui)
	if err != nil {
		t.Fatalf("Shell dry-run-vm: %v", err)
	}
	if !gotSkipVM {
		t.Error("prepareSandbox got skipVM=false under --dry-run-vm, want true")
	}
	if len(ui.InfoCalls) == 0 || ui.InfoCalls[0] != "dry-run: Would start interactive shell session" {
		t.Errorf("info = %v, want dry-run shell message", ui.InfoCalls)
	}
}

func TestShellNormalCallsRunAttach(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
			true, 0, `{"s1":{"type":"idle","attempt":0}}`, "", nil,
		),
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace"}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(
		context.Background(),
		options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)},
		options.ModeRun,
		ui,
	)
	if err != nil {
		t.Fatalf("Shell normal: %v", err)
	}
	if hasDryRunMessage(ui.InfoCalls) {
		t.Errorf("a real shell run must not take the dry-run-vm path, got info calls %v", ui.InfoCalls)
	}
}

func TestShellUsesGuestCWD(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
			true, 0, `{"s1":{"type":"idle","attempt":0}}`, "", nil,
		),
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace", guestCWD: "/workspace/sub"}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(
		context.Background(),
		options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)},
		options.ModeRun,
		ui,
	)
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if sb.AttachCwd != "/workspace/sub" {
		t.Errorf("AttachCwd = %q, want %q", sb.AttachCwd, "/workspace/sub")
	}
}

// --- runAttach error paths ---

func TestRunAttachPropagatesAttachError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachErr: errors.New("attach boom")}

	err := runAttach(context.Background(), sb, "errproj", "/workspace", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}, "-l")
	if err == nil {
		t.Fatal("expected error when attach fails")
	}
	if !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("err = %q, want wrapped attach failure", err.Error())
	}
}

func TestRunAttachReturnsExitErrorForNonZeroCode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 3}

	err := runAttach(context.Background(), sb, "exitproj", "/workspace", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}, "-l")
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("err = %v, want ExitError code 3", err)
	}
}

func TestRunAttachWarnsOnReapFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
	sb.ShellErr = errors.New("shell failed")

	// A cancelled context makes waitQuiescent abort on its next poll, so
	// reapOnLastClient returns an error that runAttach must warn about.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runAttach(ctx, sb, "reaperrproj", "/workspace", ui,
		options.RunOptions{Agent: "opencode", ReapPolicy: options.NewReapPolicy(false, 5)}, "-l")
	if err != nil {
		t.Fatalf("runAttach should not fail on reap error: %v", err)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "reap failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reap warning, got %v", ui.WarnCalls)
	}
}

func TestFinalizeRun(t *testing.T) {
	if err := finalizeRun(nil, 0); err != nil {
		t.Errorf("finalizeRun(nil, 0) = %v, want nil", err)
	}
	if err := finalizeRun(errors.New("boom"), 1); err == nil {
		t.Error("finalizeRun(error, code) should return wrapped error")
	} else if !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("finalizeRun error = %q, want wrapped session failure", err.Error())
	}
	err := finalizeRun(nil, 3)
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("finalizeRun(nil, 3) = %v, want ExitError code 3", err)
	}
}

func TestRunStartsNotifyWatcher(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	started := make(chan struct{})
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		close(started)
		return nil
	}

	err := Run(context.Background(), options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}, options.ModeRun, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("notify watcher was not started for active notify config")
	}
}

func TestRunDoesNotStartNotifyWatcherWhenInactive(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	called := false
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		called = true
		return nil
	}

	err := Run(context.Background(), options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
	}, options.ModeRun, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("notify watcher should not start when notify config is inactive")
	}
}

func TestStartNotifyWatcherNoOpWithoutSpec(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	called := false
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		called = true
		return nil
	}

	stop := startNotifyWatcher(
		context.Background(),
		msb.NewMockSandbox(msb.SandboxOpts{}),
		notify.Config{
			Desktop: true,
			Audio:   notify.AudioOff,
			OnInput: true,
			OnDone:  true,
			OnError: true,
		},
		ui,
		nil,
		"slug",
	)
	stop()
	if called {
		t.Error("startNotifyWatcher should be a no-op without an EventStreamSpec")
	}
}

func TestStartNotifyWatcherUsesSpec(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	got := make(chan agent.EventStreamSpec, 1)
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, spec agent.EventStreamSpec, _ notify.Backend) error {
		got <- spec
		return nil
	}
	spec := agent.EventStreamSpec{
		StreamCommand: "curl -N -s http://127.0.0.1:9999/events",
		BusyEvents:    []string{"busy.evt"},
		AwaitingInput: []string{"ask.evt"},
		IdleEvents:    []string{"idle.evt"},
		ErrorEvents:   []string{"fail.evt"},
		Name:          "pi",
	}
	stop := startNotifyWatcher(
		context.Background(),
		msb.NewMockSandbox(msb.SandboxOpts{}),
		notify.Config{
			Desktop: true,
			Audio:   notify.AudioOff,
			OnInput: true,
			OnDone:  true,
			OnError: true,
		},
		ui,
		&spec,
		"slug",
	)
	defer stop()
	select {
	case received := <-got:
		if received.StreamCommand != spec.StreamCommand || received.Name != spec.Name {
			t.Errorf("notifyWatch spec = %+v, want %+v", received, spec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notifyWatch was not called")
	}
}

func TestRunLogsNotifyWatcherErrorAfterStop(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		return errors.New("watch boom")
	}

	err := Run(context.Background(), options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}, options.ModeRun, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "notify watcher") && strings.Contains(v, "watch boom") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected notify watcher error logged via Verbosef after stop")
	}
}

func TestRunDoesNotStartNotifyWatcherWithoutEventStreamProvider(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	called := false
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		called = true
		return nil
	}

	err := Run(context.Background(), options.RunOptions{
		Agent:      "pi",
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}, options.ModeRun, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("notify watcher should not start for an agent without EventStreamProvider")
	}
}

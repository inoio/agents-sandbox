package journeys

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestStepSeedHostConfigDerivesFilesFromContext pins the changed step contract:
// the host files are built from the journey context at run time, so a host config
// can point at a facility an earlier step provisioned (UC4's provider baseURL is
// the stub's runtime address). It also pins where the files land: under the
// isolated HOME the first-run host probe reads.
func TestStepSeedHostConfigDerivesFilesFromContext(t *testing.T) {
	home := t.TempDir()
	ctx := &Context{TB: t, Agent: "opencode2", Home: home}
	const rel = ".config/opencode/opencode.jsonc"
	const content = `{"provider":{"stub":{}}}`

	built := false
	step := StepSeedHostConfig(func(c *Context) (map[string]string, error) {
		built = true
		if c != ctx {
			t.Errorf("the builder got context %p, want %p", c, ctx)
		}
		return map[string]string{rel: content}, nil
	})
	if err := step.Run(ctx); err != nil {
		t.Fatalf("StepSeedHostConfig: %v", err)
	}
	if !built {
		t.Fatal("the builder was not called")
	}
	// The isolated HOME is <homeBase>/home (driver.homeDirName, unexported).
	written, err := os.ReadFile(filepath.Join(home, "home", rel))
	if err != nil {
		t.Fatalf("read the seeded host config: %v", err)
	}
	if string(written) != content {
		t.Errorf("seeded content = %q, want %q", written, content)
	}
}

// TestCommandArgvAppendsNoJourneyFlags pins StepLaunchCommand's arg handling:
// the built binary and the command's own arguments, with none of the run flags
// StepLaunch adds (config learn accepts none, so an appended --agent would fail
// on an unknown flag).
func TestCommandArgvAppendsNoJourneyFlags(t *testing.T) {
	got := commandArgv("/tmp/agents-sandbox", []string{"config", "learn"})
	want := []string{"/tmp/agents-sandbox", "config", "learn"}
	if !slices.Equal(got, want) {
		t.Errorf("commandArgv = %v, want %v", got, want)
	}
}

// TestCommandEnvNamesTheJourneysAgent pins how a flagless command such as
// `config learn` targets the journey's agent: through the launcher's agent
// variable, last, so it wins over any inherited value.
func TestCommandEnvNamesTheJourneysAgent(t *testing.T) {
	ctx := &Context{Agent: "pi", Root: t.TempDir(), Home: t.TempDir()}

	env := commandEnv(ctx, []string{agentEnv + "=opencode2"})

	if got := env[len(env)-1]; got != agentEnv+"=pi" {
		t.Errorf("last env entry = %q, want %s=pi", got, agentEnv)
	}
}

func TestStepAssertStubServedPassesOnceAChatCompletionArrived(t *testing.T) {
	stub := recordingStubProvider{requests: []StubRequest{{Method: http.MethodPost, Path: "/v1/chat/completions"}}}
	ctx := &Context{TB: t, Stub: stub}

	if err := stepAssertStubServed(time.Millisecond).Run(ctx); err != nil {
		t.Fatalf("stub served a turn, step failed: %v", err)
	}
}

func TestStepAssertStubServedFailsNamingWhatTheStubSaw(t *testing.T) {
	stub := recordingStubProvider{requests: []StubRequest{{Method: http.MethodGet, Path: "/v1/models"}}}
	ctx := &Context{TB: t, Stub: stub}

	err := stepAssertStubServed(time.Millisecond).Run(ctx)

	if err == nil || !strings.Contains(err.Error(), "/v1/models") {
		t.Fatalf("error = %v, want a failure naming the requests the stub saw", err)
	}
}

func TestStepAssertStubServedNeedsAStub(t *testing.T) {
	if err := stepAssertStubServed(time.Millisecond).Run(&Context{TB: t}); err == nil {
		t.Fatal("step passed with no stub on the context")
	}
}

func TestNewReplySinceReturnsOnlyAReplySentAfterThePrompt(t *testing.T) {
	stub := repliesStub{replies: []string{"stub-reply-1", "stub-reply-2"}}

	if got, err := newReplySince(stub, 1, time.Millisecond); err != nil || got != "stub-reply-2" {
		t.Errorf("newReplySince(seen 1) = %q, %v; want stub-reply-2", got, err)
	}
	if _, err := newReplySince(stub, 2, time.Millisecond); err == nil {
		t.Error("newReplySince(seen 2) found a reply although none followed the prompt")
	}
}

func TestLaunchArgvPassesTheAdaptersLaunchArgsOnlyToARun(t *testing.T) {
	ctx := &Context{
		Binary:  "asb",
		Agent:   "claude-code",
		Harness: Harness{Drive: Driving{LaunchArgs: []string{"--mode", "x"}}},
	}

	run := launchArgv(ctx, []string{"run"}, []string{"--continue"})
	if got := run[slices.Index(run, "--")+1:]; !slices.Equal(got, []string{"--mode", "x", "--continue"}) {
		t.Errorf("run agent args = %v, want the LaunchArgs then the step's", got)
	}
	shell := launchArgv(ctx, []string{"shell"}, nil)
	if slices.Contains(shell, "--") {
		t.Errorf("shell argv = %v, want no agent args", shell)
	}
	if run[0] != "asb" || !slices.Contains(run, "--no-rebuild") ||
		run[slices.Index(run, "--agent")+1] != "claude-code" {
		t.Errorf("run argv = %v, want the binary, the agent, and --no-rebuild", run)
	}
}

// A host-config builder's error (a harness without learn facts) fails the step
// instead of seeding anything.
func TestStepSeedHostConfigReportsTheBuildersError(t *testing.T) {
	ctx := &Context{TB: t, Agent: "codex", Home: t.TempDir(), Stub: fakeStubProvider{}}

	if err := StepSeedHostConfig(uc4HostConfig).Run(ctx); err == nil || !strings.Contains(err.Error(), "learn facts") {
		t.Errorf("error = %v, want the missing learn facts named", err)
	}
}

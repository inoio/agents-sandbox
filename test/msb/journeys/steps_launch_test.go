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

// The setup step decides by whichever screen shows first: the setup marker
// (then it leaves the setup) or the readiness marker (then it does nothing).
func TestFirstShownMarkerPicksTheFirstListedMarkerTheOutputShows(t *testing.T) {
	markers := []string{"Ctrl+C exit setup", "📁 /workspace"}
	cases := map[string]struct {
		output string
		want   int
	}{
		"the setup screen":         {output: "↑/↓ select · \x1b[1mCtrl+C\x1b[0m exit setup", want: 0},
		"the editor":               {output: "status 📁  /workspace", want: 1},
		"neither yet":              {output: "booting", want: -1},
		"both, setup listed first": {output: "Ctrl+C exit setup … 📁 /workspace", want: 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := firstShownMarker(tc.output, markers); got != tc.want {
				t.Errorf("firstShownMarker(%q) = %d, want %d", tc.output, got, tc.want)
			}
		})
	}
	if got := firstShownMarker("anything", []string{""}); got != -1 {
		t.Errorf("an empty marker matched (%d); it must never match", got)
	}
}

func TestStepExitHarnessSetupDoesNothingForAHarnessWithoutOne(t *testing.T) {
	if err := StepExitHarnessSetup().Run(&Context{TB: t, Agent: opencode2AgentName, Harness: opencode2Harness}); err != nil {
		t.Errorf("StepExitHarnessSetup without a setup screen = %v, want nil before touching the session", err)
	}
}

// resubmitWhileBusy presses Enter again only while the harness shows its busy
// notice and no turn arrived, and stops on a turn or a quiet screen.
func TestResubmitWhileBusyEndsOnATurnOrAQuietScreen(t *testing.T) {
	const busy = "Startup is still in progress"
	// Each recheck shows the next screen; accepted runs first in every check,
	// so it advances the screen once per tick.
	run := func(screens []string, turnAfter int) (submits int) {
		output := ""
		next := 0
		prompt := busySubmit{
			busy:   busy,
			output: func() []byte { return []byte(output) },
			submit: func() error {
				submits++
				return nil
			},
			accepted: func() bool {
				if next < len(screens) {
					output += screens[next]
					next++
				}
				return turnAfter >= 0 && submits >= turnAfter
			},
		}
		if err := resubmitWhileBusy(prompt, time.Second, time.Millisecond); err != nil {
			t.Fatal(err)
		}
		return submits
	}
	if got := run([]string{busy, busy, "working"}, 2); got != 2 {
		t.Errorf("busy twice, then a turn: %d resubmits, want 2", got)
	}
	if got := run(nil, -1); got != 0 {
		t.Errorf("never busy: %d resubmits, want none", got)
	}
	if got := run([]string{busy, busy, busy, busy, busy}, 1); got != 1 {
		t.Errorf("a notice still drawn after the turn: %d resubmits, want 1 (the turn ends it)", got)
	}
}

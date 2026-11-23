package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectFileProblem(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journey.txt")

	if problem := projectFileProblem(path, "hello"); problem == nil || !strings.Contains(problem.Error(), "not") {
		t.Errorf("a missing file: problem = %v, want one", problem)
	}
	if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if problem := projectFileProblem(path, "hello"); problem == nil || !strings.Contains(problem.Error(), "other") {
		t.Errorf("wrong content: problem = %v, want one naming the content", problem)
	}
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if problem := projectFileProblem(path, "hello"); problem != nil {
		t.Errorf("the written file, owned by this user: problem = %v", problem)
	}
}

func TestStepAssertProjectFileReadsTheJourneysProject(t *testing.T) {
	ctx := &Context{TB: t, Project: t.TempDir()}
	if err := os.WriteFile(filepath.Join(ctx.Project, "written.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := stepAssertProjectFile("written.txt", "hi", time.Millisecond).Run(ctx); err != nil {
		t.Fatalf("a written file: %v", err)
	}
	if err := stepAssertProjectFile("never.txt", "hi", 50*time.Millisecond).Run(ctx); err == nil {
		t.Fatal("a file never written: got nil error")
	}
}

func TestExperimentalNoticeProblem(t *testing.T) {
	const notice = "claude-code is experimental — not well tested with agents-sandbox"
	cases := []struct {
		name    string
		agent   string
		output  string
		problem bool
	}{
		{name: "experimental with notice", agent: "claude-code", output: notice},
		{name: "experimental without notice", agent: "claude-code", output: "ready", problem: true},
		{name: "stable without notice", agent: "opencode2", output: "ready"},
		{name: "stable with notice", agent: "opencode2", output: "opencode2 is experimental — x", problem: true},
		{name: "unknown agent", agent: "nope", output: "", problem: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := experimentalNoticeProblem(tc.agent, tc.output); (got != nil) != tc.problem {
				t.Errorf("problem = %v, want problem %v", got, tc.problem)
			}
		})
	}
}

func TestReconfigPromptProblem(t *testing.T) {
	if reconfigPromptProblem("ready") != nil {
		t.Error("no prompt: got a problem")
	}
	prompt := "agent config changed\nApplying requires restarting the server; 1 active client(s) should reconnect"
	if reconfigPromptProblem(prompt) == nil {
		t.Error("the restart prompt: got no problem")
	}
}

func TestStepUseClientSwitchesTheActiveClient(t *testing.T) {
	ctx := &Context{TB: t}
	if err := StepUseClient(1).Run(ctx); err == nil {
		t.Fatal("no clients: got nil error")
	}
}

func TestStepScriptWriteFileArmsTheAdaptersCall(t *testing.T) {
	stub := &armingStub{}
	ctx := &Context{TB: t, Stub: stub, Harness: opencode2Harness}

	if err := StepScriptWriteFile("journey.txt", "hi").Run(ctx); err != nil {
		t.Fatalf("StepScriptWriteFile: %v", err)
	}
	if stub.call.Name != "write" || !strings.Contains(stub.call.Arguments, `"/workspace/journey.txt"`) {
		t.Errorf("armed call = %+v, want opencode's write of /workspace/journey.txt", stub.call)
	}
	if err := StepScriptWriteFile("x", "y").Run(&Context{TB: t, Stub: stub}); err == nil {
		t.Error("an adapter without WriteFileCall: got nil error")
	}
}

type armingStub struct {
	fakeStubProvider

	call ToolCall
}

func (a *armingStub) ScriptToolCall(call ToolCall) { a.call = call }

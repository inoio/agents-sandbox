package journeys

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func shellLoginContext(t *testing.T, name, command string) *Context {
	t.Helper()
	root := t.TempDir()
	binary := filepath.Join(root, "launcher")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = shell ] || exit 91\n" +
		"printf 'dev@journey $ '\n" +
		"IFS= read -r command\n" +
		"[ \"$command\" = " + shellQuote(command+"; exit") + " ] || exit 92\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Context{
		TB: t, Root: root, Home: t.TempDir(), Project: root, Binary: binary,
		Agent: name, Harness: Harnesses[name],
	}
}

func assertShellLoginExited(t *testing.T, c *Context) {
	t.Helper()
	code, err := c.Session.WaitExit(3 * time.Second)
	if err != nil || code != 0 {
		t.Fatalf("login launcher exited %d (%v), output: %s", code, err, c.Session.OutputTail())
	}
}

// fakeStubProvider is a StubProvider for a step test: StepSeedLearnSession only
// needs one present, and it reads the advertised host from the package's
// stubHost rather than the provider.
type fakeStubProvider struct{}

func (fakeStubProvider) Addr() string { return "http://stub.invalid" }

func (fakeStubProvider) Requests() []StubRequest { return nil }

func (fakeStubProvider) ScriptToolCall(ToolCall) {}

func (fakeStubProvider) Replies() []string { return nil }

func (fakeStubProvider) Stop() {}

// recordingStubProvider is a StubProvider whose served requests a test sets.
type recordingStubProvider struct {
	fakeStubProvider

	requests []StubRequest
}

func (r recordingStubProvider) Requests() []StubRequest { return r.requests }

// repliesStub is a StubProvider whose sent replies a test sets.
type repliesStub struct {
	fakeStubProvider

	replies []string
}

func (r repliesStub) Replies() []string { return r.replies }

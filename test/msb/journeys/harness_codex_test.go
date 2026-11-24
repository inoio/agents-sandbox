package journeys

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// codex writes through exec_command with a shell command line; the line must
// write exactly the content, apostrophes included.
func TestCodexWriteFileCallWritesTheContentThroughTheShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "it's.txt")
	call := codexHarness.Drive.WriteFileCall(path, "the stub's tool call")
	var arguments map[string]string
	if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
		t.Fatalf("arguments %q: %v", call.Arguments, err)
	}
	if call.Name != "exec_command" || len(arguments) != 1 || arguments["cmd"] == "" {
		t.Fatalf("call = %s(%v), want exec_command with only cmd", call.Name, arguments)
	}

	//testenv:allow running the command line the stub hands codex
	if out, err := exec.CommandContext(t.Context(), "sh", "-c", arguments["cmd"]).CombinedOutput(); err != nil {
		t.Fatalf("sh -c %q: %v\n%s", arguments["cmd"], err, out)
	}
	if problem := projectFileProblem(path, "the stub's tool call"); problem != nil {
		t.Error(problem)
	}
}

func TestCodexStubConfigSelectsAResponsesProviderAtTheStub(t *testing.T) {
	snippets := codexHarness.Stub.Snippets("http://stub.test:1234")
	content := snippets[codexStubSnippet]
	profile, _ := agent.Lookup(codexAgentName)
	merger, ok := profile.(agent.ConfigMerger)
	if !ok {
		t.Fatal("codex does not merge config snippets")
	}
	pattern := merger.SnippetPattern()
	if matched, err := filepath.Match(pattern, codexStubSnippet); err != nil || !matched {
		t.Errorf("snippet name %q does not match codex's snippet pattern %q", codexStubSnippet, pattern)
	}
	for _, want := range []string{`model = "stub"`, `model_provider = "stub"`, `base_url = "http://stub.test:1234/v1"`, `wire_api = "responses"`} {
		if !strings.Contains(content, want) {
			t.Errorf("snippet = %q, want %s", content, want)
		}
	}
}

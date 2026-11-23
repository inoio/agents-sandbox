package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/firstrun"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestStepAssertFirstRunRecorded pins the UC1 first-run-decision assertion: it
// passes for the "<repo slug>/<agent>" key carrying the recorded learning
// choice, and fails for a missing file, a missing key, or a mismatched choice.
// It reads the isolated root state directly, so the test only needs a project
// dir for the identity and a hand-written state file.
func TestStepAssertFirstRunRecorded(t *testing.T) {
	const agentName = "opencode2"

	newContext := func(t *testing.T) *Context {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
			t.Fatalf("create project dir: %v", err)
		}
		return &Context{TB: t, Root: root, Agent: agentName, Home: t.TempDir()}
	}
	keyFor := func(t *testing.T, c *Context) string {
		t.Helper()
		identity, err := journeyIdentity(c)
		if err != nil {
			t.Fatalf("journeyIdentity: %v", err)
		}
		return identity.RepoSlug + "/" + c.Agent
	}
	writeRootState := func(t *testing.T, c *Context, body string) {
		t.Helper()
		path := firstRunStateFile(c)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create state dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	t.Run("a matching record passes", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: learned\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c); err != nil {
			t.Errorf("matching record: %v", err)
		}
	})

	t.Run("the host-seed choice records the same decision", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: learned\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnFromHost).Run(c); err != nil {
			t.Errorf("host-seed choice: %v", err)
		}
	})

	t.Run("the manual choice records the manual decision", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: manual\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceManual).Run(c); err != nil {
			t.Errorf("manual choice: %v", err)
		}
	})

	t.Run("a missing root state fails", func(t *testing.T) {
		err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(newContext(t))
		if err == nil {
			t.Fatal("missing root state file: got nil error, want failure")
		}
		if !strings.Contains(err.Error(), "recorded no first-run decision") {
			t.Errorf("error = %v, want the missing-state failure", err)
		}
	})

	t.Run("a missing key fails", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs: {}\n")
		err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c)
		if err == nil {
			t.Fatal("missing key: got nil error, want failure")
		}
		if !strings.Contains(err.Error(), "recorded no first-run decision under") {
			t.Errorf("error = %v, want the missing-key failure", err)
		}
	})

	t.Run("a malformed state file fails at the parse", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "first_runs: [unclosed\n")
		err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c)
		if err == nil {
			t.Fatal("malformed state: got nil error, want failure")
		}
		if !strings.Contains(err.Error(), "parse") {
			t.Errorf("error = %v, want the parse failure", err)
		}
	})

	t.Run("a choice no path records fails", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs: {}\n")
		err := StepAssertFirstRunRecorded(firstrun.Choice("bogus")).Run(c)
		if err == nil {
			t.Fatal("an unmapped choice: got nil error, want failure")
		}
		if !strings.Contains(err.Error(), "records no RootState decision") {
			t.Errorf("error = %v, want the unmapped-choice failure", err)
		}
	})

	t.Run("a mismatched choice fails", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: manual\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c); err == nil {
			t.Error("mismatched choice: got nil error, want failure")
		}
	})
}

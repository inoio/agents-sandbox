package projecttrust

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func changedDecision() Decision {
	current := projectionOf(Input{
		Path:    "/repo/.agents-sandbox/config.yaml",
		Kind:    KindLauncherConfig,
		Entries: []string{"mounts./host: \"/\x1b[2J\""},
	})
	return Evaluate(current, toApproval(Projection{}))
}

// TestApproveRecordsTheProjectionOnApproval pins the interactive path: the
// prompt shows every change escaped, and approving records the projection so
// the next evaluation is approved.
func TestApproveRecordsTheProjectionOnApproval(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	var prompt string
	ui.SelectFn = func(message string, _ []termio.Choice, defaultKey string) (string, error) {
		prompt = message
		if defaultKey != declineKey {
			t.Errorf("default = %q, want the decline key", defaultKey)
		}
		return approveKey, nil
	}
	decision := changedDecision()

	if err := Approve(ui, "checkout-a", decision); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	for _, want := range []string{"/repo/.agents-sandbox/config.yaml", `+ mounts./host: "/\x1b[2J"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt %q does not show %q", prompt, want)
		}
	}
	approval, found, err := LoadApproval("checkout-a")
	if err != nil || !found {
		t.Fatalf("LoadApproval = %v, %v", found, err)
	}
	if !Evaluate(decision.Current, approval).Approved {
		t.Error("the recorded approval does not approve the projection it was given for")
	}
}

func TestApproveDeclinedRecordsNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return declineKey, nil }

	if err := Approve(
		ui,
		"checkout-a",
		changedDecision(),
	); !errors.Is(err, ErrDeclined) ||
		!errors.Is(err, termio.ErrCancelled) {
		t.Fatalf("Approve = %v, want ErrDeclined as a cancellation", err)
	}
	if _, found, _ := LoadApproval("checkout-a"); found {
		t.Error("a declined approval was recorded")
	}
}

func TestApprovePropagatesAPromptFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	promptErr := errors.New("stdin closed")
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return approveKey, promptErr }

	if err := Approve(ui, "checkout-a", changedDecision()); !errors.Is(err, promptErr) {
		t.Fatalf("Approve = %v, want the prompt error", err)
	}
	if _, found, _ := LoadApproval("checkout-a"); found {
		t.Error("a failed prompt recorded an approval")
	}
}

// TestApproveRefusesWithoutATerminal pins that nothing but an interactive
// answer approves: a UI without a TTY, assume-yes notwithstanding, gets an
// error naming each changed input and the remedy, and no prompt.
func TestApproveRefusesWithoutATerminal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		t.Fatal("prompted without a terminal")
		return "", nil
	}

	err := Approve(ui, "checkout-a", changedDecision())
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("Approve = %v, want ErrApprovalRequired", err)
	}
	for _, want := range []string{"/repo/.agents-sandbox/config.yaml", "interactively"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestApproveAcceptsAnApprovedDecisionSilently(t *testing.T) {
	ui := &termio.Mock{}
	if err := Approve(ui, "checkout-a", Decision{Approved: true}); err != nil {
		t.Fatalf("Approve(approved) = %v", err)
	}
}

func TestIgnoredWarningNamesTheChangedInputs(t *testing.T) {
	got := IgnoredWarning(changedDecision())
	for _, want := range []string{"not approved", "ignored", "/repo/.agents-sandbox/config.yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q does not mention %q", got, want)
		}
	}
}

// TestApproveSettingWriteKeepsTheLaunchersOwnChangeApproved pins that a
// launcher write to the project config keeps the layer approved, while a change
// the agent made to the same file meanwhile still needs approval.
func TestApproveSettingWriteKeepsTheLaunchersOwnChangeApproved(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "Dockerfile", "FROM base\n")
	path := f.write(t, "config.yaml", "network:\n  profile: public\nmemory: 4G\n")
	if err := RecordApproval("checkout-a", f.compute(t)); err != nil {
		t.Fatal(err)
	}

	f.write(t, "config.yaml", "network:\n  profile: public\nmemory: 6144M\n")
	if _, err := ApproveSettingWrite("checkout-a", path, "memory", "6144M"); err != nil {
		t.Fatalf("ApproveSettingWrite: %v", err)
	}
	approval, found, err := LoadApproval("checkout-a")
	if err != nil || !found {
		t.Fatalf("LoadApproval = %v, %v", found, err)
	}
	if got := Evaluate(f.compute(t), approval); !got.Approved {
		t.Fatalf("after the launcher's write: %+v, want approved", got)
	}

	f.write(t, "config.yaml", "network:\n  profile: public\nmemory: 8G\nmounts:\n  /host: /\n")
	if _, err := ApproveSettingWrite("checkout-a", path, "memory", "8G"); err != nil {
		t.Fatalf("ApproveSettingWrite: %v", err)
	}
	approval, _, _ = LoadApproval("checkout-a")
	got := Evaluate(f.compute(t), approval)
	want := []string{`+ mounts./host: "/"`}
	if got.Approved || len(got.Changes) != 1 || !slices.Equal(got.Changes[0].Lines, want) {
		t.Errorf("after an agent edit: %+v, want only the agent's mount to need approval", got)
	}
}

func TestApproveSettingWriteWithoutAnApprovalRecordsNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	restore, err := ApproveSettingWrite("checkout-a", "/repo/.agents-sandbox/config.yaml", "memory", "8G")
	if err != nil {
		t.Fatalf("ApproveSettingWrite: %v", err)
	}
	restore()
	if _, found, _ := LoadApproval("checkout-a"); found {
		t.Error("an approval was created for a layer the user never approved")
	}
}

// TestApproveSettingWriteRestoresThePreviousApproval pins the undo for a write
// that fails after its approval was recorded.
func TestApproveSettingWriteRestoresThePreviousApproval(t *testing.T) {
	f := newProjectFixture(t)
	path := f.write(t, "config.yaml", "memory: 4G\n")
	if err := RecordApproval("checkout-a", f.compute(t)); err != nil {
		t.Fatal(err)
	}
	before, _, _ := LoadApproval("checkout-a")

	restore, err := ApproveSettingWrite("checkout-a", path, "memory", "6144M")
	if err != nil {
		t.Fatalf("ApproveSettingWrite: %v", err)
	}
	restore()
	if after, _, _ := LoadApproval("checkout-a"); after.Digest != before.Digest {
		t.Errorf("approval digest after restore = %s, want the previous %s", after.Digest, before.Digest)
	}
}

// TestApprovalStateFailuresAreReported pins that an unreadable root state is
// reported by every approval read and write rather than taken as no approval.
func TestApprovalStateFailuresAreReported(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteFile(t, configpaths.Get().UserStateDir(), "state.yaml", "schema: [not-valid")

	if _, found, err := LoadApproval("checkout-a"); found || !errors.Is(err, state.ErrStateUnparsable) {
		t.Errorf("LoadApproval = %v, %v; want no approval and the state error", found, err)
	}
	if err := RecordApproval(
		"checkout-a",
		projectionOf(Input{Path: "/p", Kind: KindFile}),
	); !errors.Is(
		err,
		state.ErrStateUnparsable,
	) {
		t.Errorf("RecordApproval = %v, want the state error", err)
	}
	if _, err := ApproveSettingWrite(
		"checkout-a",
		"/p/config.yaml",
		"memory",
		"8G",
	); !errors.Is(
		err,
		state.ErrStateUnparsable,
	) {
		t.Errorf("ApproveSettingWrite = %v, want the state error", err)
	}
}

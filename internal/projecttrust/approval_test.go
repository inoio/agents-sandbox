package projecttrust

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// currentProjectInput is the project config an approval prompt lists.
func currentProjectInput() Input {
	return Input{
		Path:    "/repo/.agents-sandbox/config.yaml",
		Kind:    KindLauncherConfig,
		Entries: []string{`mounts./host: "/\x1b[2J"`},
	}
}

// changedDecision is a decision that differs from a previous approval, so it
// reads as a change rather than a first approval.
func changedDecision() Decision {
	approved := projectionOf(Input{
		Path:    "/repo/.agents-sandbox/config.yaml",
		Kind:    KindLauncherConfig,
		Entries: []string{`network.profile: "none"`},
	})
	return Evaluate(projectionOf(currentProjectInput()), toApproval(approved))
}

// firstApprovalDecision is a decision for a checkout with nothing approved yet.
func firstApprovalDecision() Decision {
	return Evaluate(projectionOf(currentProjectInput()), state.ApprovedProjectLayer{})
}

// TestApproveRecordsTheProjectionOnApproval pins the interactive path: the
// prompt shows every change escaped, and approving records the projection so
// the next evaluation is approved.
func TestApproveRecordsTheProjectionOnApproval(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{IsInteractiveResult: true}
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
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{IsInteractiveResult: true}
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
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{IsInteractiveResult: true}
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
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
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
	ui := &termiotest.Mock{}
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

// TestFirstApprovalMessagingNamesNewConfiguration pins that a checkout with
// nothing approved is asked to accept new configuration, not told that
// something changed since a previous approval that never happened.
func TestFirstApprovalMessagingNamesNewConfiguration(t *testing.T) {
	decision := firstApprovalDecision()
	if !decision.FirstApproval {
		t.Fatal("a decision with no recorded approval is not a first approval")
	}
	rendered := RenderChanges(decision)
	if strings.Contains(rendered, "changed since you last approved") {
		t.Errorf("first-approval prompt reads as a change:\n%s", rendered)
	}
	for _, want := range []string{
		"new configuration", "not approved", "/repo/.agents-sandbox/config.yaml", `+ mounts./host: "/\x1b[2J"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("first-approval prompt %q does not mention %q", rendered, want)
		}
	}
	warning := IgnoredWarning(decision)
	if strings.Contains(warning, "changed") || !strings.Contains(warning, "not approved yet") {
		t.Errorf("first-approval warning reads as a change:\n%s", warning)
	}
	err := Approve(&termiotest.Mock{}, "checkout-a", decision)
	if !errors.Is(err, ErrApprovalRequired) || !strings.Contains(err.Error(), "no approved project config yet") {
		t.Errorf("first-approval refusal = %v, want ErrApprovalRequired naming the missing approval", err)
	}
}

// TestChangedDecisionMessagingNamesTheChange pins the other side: a decision
// that differs from a recorded approval still reads as a change.
func TestChangedDecisionMessagingNamesTheChange(t *testing.T) {
	decision := changedDecision()
	if decision.FirstApproval {
		t.Fatal("a decision with a recorded approval is a first approval")
	}
	rendered := RenderChanges(decision)
	if !strings.Contains(rendered, "changed since you last approved it") {
		t.Errorf("changed prompt does not read as a change:\n%s", rendered)
	}
	if !strings.Contains(IgnoredWarning(decision), "changed and is not approved") {
		t.Errorf("changed warning does not read as a change:\n%s", IgnoredWarning(decision))
	}
}

// TestFirstRunChangeListNamesAConfigFileOnce pins F5 end to end: a first-run
// decision computed from a real config file without home: keys is one input, so
// the change list and every flat path list name its path once — the empty home
// manifest must not add a duplicate line or path. It drives Compute over a
// fixture, so removing the empty-entry guard in inputCollector.add (which would
// project a second, blank home input for the file) fails it.
func TestFirstRunChangeListNamesAConfigFileOnce(t *testing.T) {
	f := newProjectFixture(t)
	path := f.write(t, "config.yaml", "memory: 8G\n")
	decision := Evaluate(f.compute(t), toApproval(Projection{}))

	rendered := RenderChanges(decision)
	if got := strings.Count(rendered, path); got != 1 {
		t.Errorf("RenderChanges names %q %d times, want once:\n%s", path, got, rendered)
	}
	for _, flat := range []string{changedPaths(decision.Changes), IgnoredWarning(decision)} {
		if got := strings.Count(flat, path); got != 1 {
			t.Errorf("%q names %q %d times, want once", flat, path, got)
		}
	}
	err := Approve(&termiotest.Mock{}, "checkout-a", decision)
	if !errors.Is(err, ErrApprovalRequired) || strings.Count(err.Error(), path) != 1 {
		t.Errorf("non-interactive refusal = %v, want ErrApprovalRequired naming the path once", err)
	}
}

// TestApproveSettingWriteKeepsTheLaunchersOwnChangeApproved pins that a
// launcher write to the project config keeps the layer approved, while a change
// the agent made to the same file meanwhile still needs approval.
func TestApproveSettingWriteKeepsTheLaunchersOwnChangeApproved(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "Dockerfile", "FROM base\n")
	f.write(t, "config.yaml", "network:\n  profile: public\nmemory: 4G\n")
	if err := RecordApproval("checkout-a", f.compute(t)); err != nil {
		t.Fatal(err)
	}

	f.write(t, "config.yaml", "network:\n  profile: public\nmemory: 6144M\n")
	if _, err := ApproveSettingWrite("checkout-a", "memory", "6144M"); err != nil {
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
	if _, err := ApproveSettingWrite("checkout-a", "memory", "8G"); err != nil {
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
	configpathstest.WithMockConfigPaths(t)
	restore, err := ApproveSettingWrite("checkout-a", "memory", "8G")
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
	f.write(t, "config.yaml", "memory: 4G\n")
	if err := RecordApproval("checkout-a", f.compute(t)); err != nil {
		t.Fatal(err)
	}
	before, _, _ := LoadApproval("checkout-a")

	restore, err := ApproveSettingWrite("checkout-a", "memory", "6144M")
	if err != nil {
		t.Fatalf("ApproveSettingWrite: %v", err)
	}
	restore()
	if after, _, _ := LoadApproval("checkout-a"); after.Digest != before.Digest {
		t.Errorf("approval digest after restore = %s, want the previous %s", after.Digest, before.Digest)
	}
}

// TestApproveSecretWriteKeepsTheLaunchersOwnRemovalApproved pins that the
// launcher removing a project secret extends the recorded approval by only that
// env-var, so the next run does not gate the launcher's own change.
func TestApproveSecretWriteKeepsTheLaunchersOwnRemovalApproved(t *testing.T) {
	f := newProjectFixture(t)
	// A second project input keeps the projection non-empty after the secret is
	// removed, so the removal is a real change the approval must absorb (an empty
	// projection is approved regardless).
	f.write(t, "config.yaml", "memory: 4G\n")
	f.write(t, "env.secret.yaml", "WIDGET_TOKEN:\n  value: sk-project\n  host: api.widget.example\n")
	if err := RecordApproval("checkout-a", f.compute(t)); err != nil {
		t.Fatal(err)
	}

	// The launcher removed the entry; the file is left present without it.
	f.write(t, "env.secret.yaml", "{}\n")
	if _, err := ApproveSecretWrite("checkout-a", "WIDGET_TOKEN"); err != nil {
		t.Fatalf("ApproveSecretWrite: %v", err)
	}
	approval, found, err := LoadApproval("checkout-a")
	if err != nil || !found {
		t.Fatalf("LoadApproval = %v, %v", found, err)
	}
	if got := Evaluate(f.compute(t), approval); !got.Approved {
		t.Fatalf("after the launcher's removal: %+v, want approved", got)
	}
}

// TestApproveSecretWriteStillGatesAnUnrelatedChange pins the scoping: extending
// the approval by one removed env-var must not approve a change to another
// secret in the same file.
func TestApproveSecretWriteStillGatesAnUnrelatedChange(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "env.secret.yaml",
		"WIDGET_TOKEN:\n  value: sk\n  host: api.widget.example\n"+
			"OTHER_TOKEN:\n  value: sk2\n  host: api.other.example\n")
	if err := RecordApproval("checkout-a", f.compute(t)); err != nil {
		t.Fatal(err)
	}

	// WIDGET_TOKEN is removed and OTHER_TOKEN's host is changed by the agent.
	f.write(t, "env.secret.yaml", "OTHER_TOKEN:\n  value: sk2\n  host: api.changed.example\n")
	if _, err := ApproveSecretWrite("checkout-a", "WIDGET_TOKEN"); err != nil {
		t.Fatalf("ApproveSecretWrite: %v", err)
	}
	approval, _, _ := LoadApproval("checkout-a")
	if got := Evaluate(f.compute(t), approval); got.Approved {
		t.Fatalf("an unrelated secret change was approved: %+v", got)
	}
}

func TestApproveSecretWriteWithoutAnApprovalRecordsNothing(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	restore, err := ApproveSecretWrite("checkout-a", "WIDGET_TOKEN")
	if err != nil {
		t.Fatalf("ApproveSecretWrite: %v", err)
	}
	restore()
	if _, found, _ := LoadApproval("checkout-a"); found {
		t.Error("an approval was created for a layer the user never approved")
	}
}

// TestApproveSecretWriteRestoresThePreviousApproval pins the undo for a removal
// that fails after its approval was recorded.
func TestApproveSecretWriteRestoresThePreviousApproval(t *testing.T) {
	f := newProjectFixture(t)
	f.write(t, "env.secret.yaml", "WIDGET_TOKEN:\n  value: sk\n  host: api.widget.example\n")
	if err := RecordApproval("checkout-a", f.compute(t)); err != nil {
		t.Fatal(err)
	}
	before, _, _ := LoadApproval("checkout-a")

	restore, err := ApproveSecretWrite("checkout-a", "WIDGET_TOKEN")
	if err != nil {
		t.Fatalf("ApproveSecretWrite: %v", err)
	}
	restore()
	if after, _, _ := LoadApproval("checkout-a"); after.Digest != before.Digest {
		t.Errorf("approval digest after restore = %s, want the previous %s", after.Digest, before.Digest)
	}
}

// TestApprovalStateFailuresAreReported pins that an unreadable root state is
// reported by every approval read and write rather than taken as no approval.
func TestApprovalStateFailuresAreReported(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
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
		"memory",
		"8G",
	); !errors.Is(
		err,
		state.ErrStateUnparsable,
	) {
		t.Errorf("ApproveSettingWrite = %v, want the state error", err)
	}
}

package learn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/analyser"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// stubReadBack swaps the read-back seam so a prompt test drives the exit
// analysis without a real home volume.
func stubReadBack(t *testing.T, result readback.ReadBack) {
	t.Helper()
	orig := readBack
	readBack = func(string, string, readback.Manifest) (readback.ReadBack, error) {
		return result, nil
	}
	t.Cleanup(func() { readBack = orig })
}

// choosingUI returns an interactive UI whose Select answers choice.
func choosingUI(choice string) *termiotest.Mock {
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return choice, nil
	}
	return ui
}

func TestExitPromptReviewAppliesPerFinding(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	stubReadBack(t, readback.ReadBack{})

	var acceptAll *bool
	orig := applyFindings
	applyFindings = func(_ []analyser.Finding, opts analyser.ApplyOptions) []analyser.ApplyResult {
		accepted := opts.AcceptAll
		acceptAll = &accepted
		return nil
	}
	t.Cleanup(func() { applyFindings = orig })

	_, err := runExitPrompt(context.Background(), Analysis{}, choosingUI(exitReview), options.ModeRun, nil)
	if err != nil {
		t.Fatalf("runExitPrompt: %v", err)
	}
	if acceptAll == nil {
		t.Fatal("Review and save did not run the applier")
	}
	if *acceptAll {
		t.Error("Review and save must apply per finding, so AcceptAll must be false")
	}
}

func TestExitPromptSaveAllAppliesWithoutAsking(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	stubReadBack(t, readback.ReadBack{})

	var acceptAll *bool
	orig := applyFindings
	applyFindings = func(_ []analyser.Finding, opts analyser.ApplyOptions) []analyser.ApplyResult {
		accepted := opts.AcceptAll
		acceptAll = &accepted
		return nil
	}
	t.Cleanup(func() { applyFindings = orig })

	_, err := runExitPrompt(context.Background(), Analysis{}, choosingUI(exitSaveAll), options.ModeRun, nil)
	if err != nil {
		t.Fatalf("runExitPrompt: %v", err)
	}
	if acceptAll == nil {
		t.Fatal("Save all did not run the applier")
	}
	if !*acceptAll {
		t.Error("Save all must apply every finding, so AcceptAll must be true")
	}
}

func TestExitPromptNotNowWritesNothing(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	applied := false
	orig := applyFindings
	applyFindings = func([]analyser.Finding, analyser.ApplyOptions) []analyser.ApplyResult {
		applied = true
		return nil
	}
	t.Cleanup(func() { applyFindings = orig })

	if _, err := runExitPrompt(
		context.Background(),
		Analysis{},
		choosingUI(exitNotNow),
		options.ModeRun,
		nil,
	); err != nil {
		t.Fatalf("runExitPrompt: %v", err)
	}
	if applied {
		t.Error("Not now must not run the applier")
	}
}

func TestExitPromptNonInteractivePrintsOneLine(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	stubReadBack(t, readback.ReadBack{})
	ui := &termiotest.Mock{} // non-interactive

	if _, err := runExitPrompt(context.Background(), Analysis{}, ui, options.ModeRun, nil); err != nil {
		t.Fatalf("runExitPrompt: %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the one-line notice", ui.AcknowledgeCalls)
	}
	if !strings.Contains(ui.AcknowledgeCalls[0], "run --learn") {
		t.Errorf("notice %q, want it to name `run --learn`", ui.AcknowledgeCalls[0])
	}
}

// TestFinishRecordsTheArtefactPath pins that a saved capture's path reaches the
// session record, so config learn can load it when the analysis did not finish.
// The analysis is forced to fail, which is exactly the window where the record
// (and its path) must survive.
func TestFinishRecordsTheArtefactPath(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedHomeVolume(t, k, "vol-1")
	stubLearnReadback(t, "/home/vol", readback.Manifest{})

	origRead := readBack
	readBack = func(string, string, readback.Manifest) (readback.ReadBack, error) {
		return readback.ReadBack{}, errors.New("read-back boom")
	}
	t.Cleanup(func() { readBack = origRead })

	run, err := Begin(network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := run.CaptureBaseline(context.Background(), k, "opencode"); err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}

	finishErr := run.Finish(context.Background(), FinishInput{
		Key:   k,
		Slug:  "proj",
		Agent: "opencode",
		Paths: configpaths.Get(),
		Home:  "/home/vol",
		UI:    &termiotest.Mock{},
		Mode:  options.ModeRun,
	})
	if finishErr == nil {
		t.Fatal("Finish = nil error, want the forced analysis failure")
	}

	record, ok, err := state.ReadLearnSession(k)
	if err != nil || !ok {
		t.Fatalf("ReadLearnSession = ok=%v err=%v, want the surviving record", ok, err)
	}
	if record.ArtefactPath == "" {
		t.Error("ArtefactPath was not recorded; config learn could not load the capture")
	}
}

package learn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/analyser"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// stubReadBack swaps the read-back seam so a prompt test drives the exit
// analysis without a real home volume.
func stubReadBack(t *testing.T) {
	t.Helper()
	orig := readBack
	readBack = func(string, string, readback.Manifest) (readback.ReadBack, error) {
		return readback.ReadBack{}, nil
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
	stubReadBack(t)

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
	stubReadBack(t)

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
	stubReadBack(t)
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

func runWithRecordedBaseline(t *testing.T, key state.Key, observation channel.Observation) *Run {
	t.Helper()
	configpathstest.WithMockConfigPaths(t)
	seedHomeVolume(t, key, "vol-1")
	stubLearnReadback(t, "/home/vol", readback.Manifest{})

	originalStartObservation := startObservation
	startObservation = func(string, network.Policy, time.Time) (channel.Observer, error) {
		return fixedObserver{observation: observation}, nil
	}
	t.Cleanup(func() { startObservation = originalStartObservation })

	run, err := Begin(network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := run.CaptureBaseline(context.Background(), key, key.Agent); err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}
	return run
}

func TestPreserveForReplaySavesObservationAndKeepsSessionRecord(t *testing.T) {
	k := state.Key{Slug: "proj", Agent: "opencode"}
	wantObservation := channel.Observation{
		ProxyLog:      "/state/learn/proxy-logs/1-session.log",
		ProxyWarnings: 1,
	}
	run := runWithRecordedBaseline(t, k, wantObservation)

	ui := &termiotest.Mock{}
	if err := run.PreserveForReplay(ui); err != nil {
		t.Fatalf("PreserveForReplay: %v", err)
	}

	record, ok, err := state.ReadLearnSession(k)
	if err != nil || !ok {
		t.Fatalf("ReadLearnSession = ok=%v err=%v, want the replay record", ok, err)
	}
	if record.ArtefactPath == "" {
		t.Fatal("ArtefactPath is empty, so config learn cannot replay the observation")
	}
	gotObservation, err := channel.Load(record.ArtefactPath)
	if err != nil {
		t.Fatalf("Load preserved observation: %v", err)
	}
	if gotObservation.ProxyLog != wantObservation.ProxyLog ||
		gotObservation.ProxyWarnings != wantObservation.ProxyWarnings {
		t.Errorf(
			"preserved observation = %+v, want proxy log %q and one warning",
			gotObservation,
			wantObservation.ProxyLog,
		)
	}
	if !warnContains(ui, wantObservation.ProxyLog) {
		t.Errorf("WarnCalls = %v, want the proxy log path", ui.WarnCalls)
	}
}

func TestPreserveForReplaySkipsPassiveAndAlreadySavedCaptures(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	saved := false
	originalSave := saveArtefact
	saveArtefact = func(string, int64, channel.Observation) (string, error) {
		saved = true
		return "/state/capture.json", nil
	}
	t.Cleanup(func() { saveArtefact = originalSave })

	if err := Passive(network.Policy{}).PreserveForReplay(nil); err != nil {
		t.Fatalf("PreserveForReplay on a passive run: %v", err)
	}
	alreadySaved := &Run{
		observer:        fixedObserver{},
		sessionRecorded: true,
		record:          state.LearnSession{ArtefactPath: "/state/capture.json"},
	}
	if err := alreadySaved.PreserveForReplay(nil); err != nil {
		t.Fatalf("PreserveForReplay on an already saved run: %v", err)
	}
	if saved {
		t.Error("PreserveForReplay rewrote an unavailable or already saved capture")
	}
}

func TestPreserveForReplayPropagatesSaveFailure(t *testing.T) {
	key := state.Key{Slug: "proj", Agent: "opencode"}
	run := runWithRecordedBaseline(t, key, channel.Observation{})
	originalSave := saveArtefact
	saveArtefact = func(string, int64, channel.Observation) (string, error) {
		return "", errors.New("save boom")
	}
	t.Cleanup(func() { saveArtefact = originalSave })

	err := run.PreserveForReplay(&termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), "save the learn capture for replay") {
		t.Fatalf("PreserveForReplay error = %v, want the wrapped save failure", err)
	}
}

func TestPreserveForReplayPropagatesRecordFailure(t *testing.T) {
	key := state.Key{Slug: "proj", Agent: "opencode"}
	run := runWithRecordedBaseline(t, key, channel.Observation{})
	originalSave, originalRecord := saveArtefact, recordLearnSession
	saveArtefact = func(string, int64, channel.Observation) (string, error) {
		return filepath.Join(t.TempDir(), "capture.json"), nil
	}
	recordLearnSession = func(state.Key, state.LearnSession) error { return errors.New("record boom") }
	t.Cleanup(func() { saveArtefact, recordLearnSession = originalSave, originalRecord })

	err := run.PreserveForReplay(&termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), "record the learn capture path for replay") {
		t.Fatalf("PreserveForReplay error = %v, want the wrapped record failure", err)
	}
}

// TestExitChoicesReviewSaveAllNotNow pins the three exit choices, their order,
// and that Not now is the default (last).
func TestExitChoicesReviewSaveAllNotNow(t *testing.T) {
	choices := exitChoices()
	if len(choices) != 3 {
		t.Fatalf("exitChoices = %+v, want three choices", choices)
	}
	wantKeys := []string{exitReview, exitSaveAll, exitNotNow}
	for i, want := range wantKeys {
		if choices[i].Key != want {
			t.Errorf("choice %d key = %q, want %q", i, choices[i].Key, want)
		}
	}
	if choices[2].Label != "Not now" {
		t.Errorf("last choice label = %q, want Not now (the default)", choices[2].Label)
	}
}

// TestChosenExitNilUI is Not now pins that a nil UI cannot panic and selects Not
// now, matching the non-interactive path.
func TestChosenExitNilUIisNotNow(t *testing.T) {
	choice, err := chosenExit(nil)
	if err != nil {
		t.Fatalf("chosenExit(nil): %v", err)
	}
	if choice != exitNotNow {
		t.Errorf("chosenExit(nil) = %q, want %q", choice, exitNotNow)
	}
}

// TestRunExitPromptSelectError pins that a prompt failure is wrapped and does not
// fall through to the analysis.
func TestRunExitPromptSelectError(t *testing.T) {
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return "", errors.New("select boom")
	}

	_, err := runExitPrompt(context.Background(), Analysis{}, ui, options.ModeRun, nil)
	if err == nil || !strings.Contains(err.Error(), "ask how to save the learned config") {
		t.Fatalf("runExitPrompt error = %v, want the wrapped prompt failure", err)
	}
}

// TestRunExitPromptCancelledContext pins that a cancelled context after the
// choice aborts before the analysis runs.
func TestRunExitPromptCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runExitPrompt(ctx, Analysis{}, choosingUI(exitReview), options.ModeRun, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runExitPrompt error = %v, want context.Canceled", err)
	}
}

// TestExitPromptInteractiveNotNowSummarizesObservation pins that an interactive
// Not now writes nothing but still summarizes the observation, with no results.
func TestExitPromptInteractiveNotNowSummarizesObservation(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	applied := false
	orig := applyFindings
	applyFindings = func([]analyser.Finding, analyser.ApplyOptions) []analyser.ApplyResult {
		applied = true
		return nil
	}
	t.Cleanup(func() { applyFindings = orig })

	summary, err := runExitPrompt(
		context.Background(),
		Analysis{Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example.com", Success: true},
		}}},
		choosingUI(exitNotNow),
		options.ModeRun,
		nil,
	)
	if err != nil {
		t.Fatalf("runExitPrompt: %v", err)
	}
	if applied {
		t.Error("Not now must not run the applier")
	}
	if summary.ServicesReached != 1 {
		t.Errorf("ServicesReached = %d, want 1 from the observation", summary.ServicesReached)
	}
	if summary.Results != nil {
		t.Errorf("Results = %+v, want nil for Not now", summary.Results)
	}
}

// TestPrintSummaryReportsDeniedDestinations pins the summary line and the
// separate denied note.
func TestPrintSummaryReportsDeniedDestinations(t *testing.T) {
	ui := &termiotest.Mock{}
	printSummary(ui, Summary{
		ServicesReached: 2,
		KeysUsed:        1,
		Denied:          []string{"blocked.example", "denied.example"},
	})

	if len(ui.InfoCalls) != 2 {
		t.Fatalf("InfoCalls = %v, want the summary and the denied note", ui.InfoCalls)
	}
	if !strings.Contains(ui.InfoCalls[0], "2 services reached, 1 keys used") {
		t.Errorf("summary line = %q, want the counts", ui.InfoCalls[0])
	}
	if !strings.Contains(ui.InfoCalls[1], "Denied destinations") ||
		!strings.Contains(ui.InfoCalls[1], "blocked.example, denied.example") {
		t.Errorf("denied note = %q, want the comma-joined denied hosts", ui.InfoCalls[1])
	}
}

// TestPrintProxyDiagnosticsReportsFailuresAndLogState pins the warning shown
// before analysis, including ambiguous attribution and incomplete-log status.
func TestPrintProxyDiagnosticsReportsFailuresAndLogState(t *testing.T) {
	ui := &termiotest.Mock{}
	PrintProxyDiagnostics(ui, channel.Observation{
		Destinations: []channel.Destination{
			{Host: "pinned.example", Protocol: channel.ConnectProtocol, Success: true, Count: 1, Failed: 1},
			{Host: "healthy.example", Protocol: channel.ConnectProtocol, Success: true, Count: 1},
			{Host: "", Protocol: channel.ConnectProtocol, Success: true, Count: 1, Failed: 1},
		},
		ProxyLog:               "/state/learn/proxy-logs/1-unique.log",
		ProxyWarnings:          3,
		UnattributedHandshakes: 1,
		ProxyLogTruncated:      true,
		ProxyLogWriteFailed:    true,
	})

	warnings := strings.Join(ui.WarnCalls, "\n")
	if strings.Contains(warnings, "healthy.example") || strings.Contains(warnings, "TLS interception failed for ,") {
		t.Errorf("WarnCalls = %v, want only named all-failed destinations", ui.WarnCalls)
	}
	for _, want := range []string{
		"pinned.example", "successful use was not verified", "Possible causes",
		"1 TLS handshake failure(s)", "could not be tied to a destination", "3",
		"/state/learn/proxy-logs/1-unique.log",
		"truncated", "incomplete",
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("WarnCalls = %v, want %q", ui.WarnCalls, want)
		}
	}

	openFailed := &termiotest.Mock{}
	PrintProxyDiagnostics(openFailed, channel.Observation{ProxyWarnings: 1, ProxyLogWriteFailed: true})
	if len(openFailed.WarnCalls) != 1 || !strings.Contains(openFailed.WarnCalls[0], "could not be created") {
		t.Errorf("WarnCalls = %v, want the open failure", openFailed.WarnCalls)
	}

	clean := &termiotest.Mock{}
	PrintProxyDiagnostics(clean, channel.Observation{})
	if len(clean.WarnCalls) != 0 {
		t.Errorf("WarnCalls = %v, want nothing for a clean session", clean.WarnCalls)
	}
	PrintProxyDiagnostics(nil, channel.Observation{ProxyWarnings: 1})

	for _, tc := range []struct {
		name        string
		observation channel.Observation
		want        string
	}{
		{
			name: "write failure",
			observation: channel.Observation{
				ProxyLog: "/state/learn/proxy-logs/2-session.log", ProxyWarnings: 1, ProxyLogWriteFailed: true,
			},
			want: "log may be incomplete",
		},
		{
			name: "truncation",
			observation: channel.Observation{
				ProxyLog: "/state/learn/proxy-logs/3-session.log", ProxyWarnings: 1, ProxyLogTruncated: true,
			},
			want: "log was truncated",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := &termiotest.Mock{}
			PrintProxyDiagnostics(ui, tc.observation)
			if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], tc.want) {
				t.Errorf("WarnCalls = %v, want %q", ui.WarnCalls, tc.want)
			}
		})
	}
}

// TestFinishPropagatesSaveFailure pins that a failed capture save aborts the
// after-run rather than analysing an unsaved session, while the proxy
// diagnostic is printed before the save can fail.
type fixedObserver struct{ observation channel.Observation }

func (fixedObserver) Overlay() network.Policy { return network.Policy{} }

func (o fixedObserver) Stop() channel.Observation { return o.observation }

func TestFinishPropagatesSaveFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}

	orig := saveArtefact
	saveArtefact = func(string, int64, channel.Observation) (string, error) {
		return "", errors.New("save boom")
	}
	t.Cleanup(func() { saveArtefact = orig })

	ui := &termiotest.Mock{}
	run := &Run{observer: fixedObserver{observation: channel.Observation{
		ProxyLog:      "/state/learn/proxy-logs/1-session.log",
		ProxyWarnings: 2,
	}}}

	finishErr := run.Finish(context.Background(), FinishInput{
		Key: k, Slug: "proj", Agent: "opencode", Paths: configpaths.Get(), UI: ui,
	})
	if finishErr == nil || !strings.Contains(finishErr.Error(), "save the learn capture") {
		t.Fatalf("Finish error = %v, want the wrapped save failure", finishErr)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "/state/learn/proxy-logs/1-session.log") {
		t.Errorf("WarnCalls = %v, want the proxy log path despite the capture save failure", ui.WarnCalls)
	}
}

// TestFinishPropagatesRecordFailure pins that a failure to record the capture
// path aborts the after-run, so config learn is not left with a record it cannot
// trust.
func TestFinishPropagatesRecordFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}

	origSave, origRecord := saveArtefact, recordLearnSession
	saveArtefact = func(string, int64, channel.Observation) (string, error) {
		return filepath.Join(t.TempDir(), "capture.json"), nil
	}
	recordLearnSession = func(state.Key, state.LearnSession) error { return errors.New("record boom") }
	t.Cleanup(func() { saveArtefact, recordLearnSession = origSave, origRecord })

	run, err := Begin(network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	finishErr := run.Finish(context.Background(), FinishInput{
		Key: k, Slug: "proj", Agent: "opencode", Paths: configpaths.Get(), UI: &termiotest.Mock{},
	})
	if finishErr == nil || !strings.Contains(finishErr.Error(), "record the learn capture path") {
		t.Fatalf("Finish error = %v, want the wrapped record failure", finishErr)
	}
}

// stubFinishAnalysis swaps the read-back and applier seams so a successful
// Finish's analysis is inert.
func stubFinishAnalysis(t *testing.T) {
	t.Helper()
	stubReadBack(t)
	orig := applyFindings
	applyFindings = func([]analyser.Finding, analyser.ApplyOptions) []analyser.ApplyResult { return nil }
	t.Cleanup(func() { applyFindings = orig })
}

// TestFinishSuccessSavesDeletesAndPrints pins a successful active after-run: the
// saved capture is deleted, the session record is cleared, and the summary is
// printed.
func TestFinishSuccessSavesDeletesAndPrints(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	stubFinishAnalysis(t)

	savedPath := ""
	origSave := saveArtefact
	saveArtefact = func(slug string, unix int64, obs channel.Observation) (string, error) {
		path, err := channel.Save(slug, unix, obs)
		savedPath = path
		return path, err
	}
	t.Cleanup(func() { saveArtefact = origSave })

	run, err := Begin(network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	ui := &termiotest.Mock{}

	if err := run.Finish(context.Background(), FinishInput{
		Key: k, Slug: "proj", Agent: "opencode", Paths: configpaths.Get(), Home: "/home/vol", UI: ui,
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	record, ok, err := state.ReadLearnSession(k)
	if err != nil {
		t.Fatalf("ReadLearnSession: %v", err)
	}
	if ok {
		t.Errorf("session record = %+v, want it cleared", record)
	}
	if savedPath == "" {
		t.Fatal("the capture was not saved")
	}
	if _, err := os.Stat(savedPath); !os.IsNotExist(err) {
		t.Errorf("capture still present after a successful Finish (err = %v)", err)
	}
	if len(ui.InfoCalls) == 0 || !strings.Contains(ui.InfoCalls[0], "learned:") {
		t.Errorf("InfoCalls = %v, want the printed summary", ui.InfoCalls)
	}
}

// TestFinishWarnsWhenDeletingTheCaptureFails pins that a failed capture delete is
// a warning, not a failed after-run. The save seam returns a non-empty directory,
// so the delete fails with ENOTEMPTY.
func TestFinishWarnsWhenDeletingTheCaptureFails(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	stubFinishAnalysis(t)

	captureDir := filepath.Join(t.TempDir(), "capture.json")
	if err := os.MkdirAll(captureDir, 0o700); err != nil {
		t.Fatalf("mkdir capture: %v", err)
	}
	testutil.WritePath(t, filepath.Join(captureDir, "keep"), "x")

	orig := saveArtefact
	saveArtefact = func(string, int64, channel.Observation) (string, error) { return captureDir, nil }
	t.Cleanup(func() { saveArtefact = orig })

	run, err := Begin(network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	ui := &termiotest.Mock{}

	if err := run.Finish(context.Background(), FinishInput{
		Key: k, Slug: "proj", Agent: "opencode", Paths: configpaths.Get(), UI: ui,
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if !warnContains(ui, "deleting the capture failed") {
		t.Errorf("WarnCalls = %v, want the delete-failure warning", ui.WarnCalls)
	}
}

// TestFinishWarnsWhenClearingTheSessionFails pins that a failed clear is a
// warning, not a failed after-run. A corrupt state document makes the clear's
// read fail.
func TestFinishWarnsWhenClearingTheSessionFails(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	stubFinishAnalysis(t)
	seedHomeVolume(t, k, "vol-1")
	testutil.WritePath(t, filepath.Join(state.KeyDir(k), "state.yaml"), "{ corrupt: [\n")

	run := Passive(network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	ui := &termiotest.Mock{}

	if err := run.Finish(context.Background(), FinishInput{
		Key: k, Slug: "proj", Agent: "opencode", Paths: configpaths.Get(), UI: ui,
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if !warnContains(ui, "clearing the session record failed") {
		t.Errorf("WarnCalls = %v, want the clear-failure warning", ui.WarnCalls)
	}
}

// warnContains reports whether any warning contains want.
func warnContains(ui *termiotest.Mock, want string) bool {
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}

// TestChosenExitDiscardsTypeAheadBeforeAsking pins that input typed for the
// agent's session and not consumed (a second Ctrl-D that quit it) cannot
// answer the exit prompt: the prompt drops it before asking.
func TestChosenExitDiscardsTypeAheadBeforeAsking(t *testing.T) {
	ui := &termiotest.Mock{IsInteractiveResult: true}
	discardedBeforeAsking := false
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		discardedBeforeAsking = ui.DiscardTypeAheadCalls == 1
		return exitSaveAll, nil
	}

	choice, err := chosenExit(ui)

	if err != nil || choice != exitSaveAll {
		t.Fatalf("chosenExit = %q, %v; want Save all", choice, err)
	}
	if !discardedBeforeAsking {
		t.Error("the exit prompt asked before dropping the type-ahead")
	}
}

// TestPrintSummaryWarnsWithAReportedFinding pins that a report-only finding,
// which the applier never asks about, shows its evidence and guidance as a
// warning after the summary line, while an applied finding stays quiet.
func TestPrintSummaryWarnsWithAReportedFinding(t *testing.T) {
	ui := &termiotest.Mock{}
	//exhaustruct:ignore // only the outcome and the rendered text matter
	reported := analyser.ApplyResult{
		Outcome: analyser.OutcomeNotApplicable,
		Finding: analyser.Finding{
			Evidence: analyser.Evidence{Summary: "An API key is stored in plain text."},
			Writes:   []analyser.Write{{Text: "Declare it as an env secret."}, {Text: ""}},
		},
	}
	//exhaustruct:ignore // only the outcome matters
	applied := analyser.ApplyResult{
		Outcome: analyser.OutcomeApplied,
		Finding: analyser.Finding{Evidence: analyser.Evidence{Summary: "applied"}},
	}

	printSummary(ui, Summary{Results: []analyser.ApplyResult{applied, reported}})

	if len(ui.InfoCalls) != 1 {
		t.Fatalf("InfoCalls = %v, want the summary line only", ui.InfoCalls)
	}
	want := []string{"An API key is stored in plain text. Declare it as an env secret."}
	if strings.Join(ui.WarnCalls, "|") != strings.Join(want, "|") {
		t.Errorf("WarnCalls = %q, want %q", ui.WarnCalls, want)
	}
}

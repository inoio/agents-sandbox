package pruning

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func compactableHandle(name string, result *msbSdk.DiskCompactionResult) *msb.MockSandboxHandle {
	return labelledHandle(name, map[string]string{naming.LabelSlug: strings.SplitN(name, "-", 2)[0]}, result)
}

func labelledHandle(name string, labels map[string]string, result *msbSdk.DiskCompactionResult) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Name_:         name,
		Status_:       msbSdk.SandboxStatusStopped,
		Cfg:           &msbSdk.SandboxConfig{Labels: labels},
		CompactResult: result,
	}
}

func rootDiskResult(inputLayers, outputLayers uint32, materializedBytes uint64) *msbSdk.DiskCompactionResult {
	return &msbSdk.DiskCompactionResult{
		InputLayers:       inputLayers,
		SelectedLayers:    inputLayers,
		OutputLayers:      outputLayers,
		MaterializedBytes: materializedBytes,
		Disks: []msbSdk.DiskCompactionDiskResult{{
			GuestPath:         "/",
			InputLayers:       inputLayers,
			SelectedLayers:    inputLayers,
			OutputLayers:      outputLayers,
			MaterializedBytes: materializedBytes,
		}},
	}
}

func TestCompactSandboxesSkipsNonProjectSandboxes(t *testing.T) {
	project := compactableHandle("myproj-abc12345", rootDiskResult(5, 1, 2048))
	task := labelledHandle(
		"myproj-task-1",
		map[string]string{
			naming.LabelSlug: "myproj",
			naming.LabelType: naming.SandboxTypeTask,
		},
		rootDiskResult(3, 1, 1),
	)
	unidentified := labelledHandle(naming.LegacyHomePrefix+"x", nil, rootDiskResult(3, 1, 1))
	msb.WithMsbMock(t, &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{task, unidentified, project}})

	layers := uint32(3)
	options := msbSdk.DiskCompactionOptions{DryRun: true, Layers: &layers, RootDiskOnly: true}
	report, err := CompactSandboxes(context.Background(), "", options, &termio.Mock{})
	if err != nil {
		t.Fatalf("CompactSandboxes: %v", err)
	}
	if len(project.CompactedOptions) != 1 || project.CompactedOptions[0] != options {
		t.Fatalf("project options = %+v, want %+v", project.CompactedOptions, options)
	}
	if len(task.CompactedOptions) != 0 || len(unidentified.CompactedOptions) != 0 {
		t.Fatal("task and unidentified sandboxes must not be compacted")
	}
	if len(report.Entries) != 1 || report.Entries[0].Name != project.Name_ || report.Entries[0].Slug != "myproj" {
		t.Fatalf("entries = %+v", report.Entries)
	}
	if !report.DryRun {
		t.Fatal("report should record dry-run")
	}
}

func TestCompactSandboxesFiltersByName(t *testing.T) {
	first := compactableHandle("first-abc12345", rootDiskResult(2, 1, 1))
	second := compactableHandle("second-abc12345", rootDiskResult(2, 1, 1))
	msb.WithMsbMock(t, &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{first, second}})

	report, err := CompactSandboxes(
		context.Background(),
		"second-abc12345",
		msbSdk.DiskCompactionOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("CompactSandboxes: %v", err)
	}
	if len(first.CompactedOptions) != 0 || len(second.CompactedOptions) != 1 || len(report.Entries) != 1 {
		t.Fatalf("only the named sandbox should be compacted, report = %+v", report)
	}
}

func TestCompactSandboxesUnknownNameFails(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{
		compactableHandle("first-abc12345", rootDiskResult(2, 1, 1)),
	}})

	_, err := CompactSandboxes(context.Background(), "missing", msbSdk.DiskCompactionOptions{}, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), `"missing"`) {
		t.Fatalf("err = %v, want unknown-sandbox error", err)
	}
}

func TestCompactSandboxesWithoutSandboxesIsEmpty(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	report, err := CompactSandboxes(context.Background(), "", msbSdk.DiskCompactionOptions{}, &termio.Mock{})
	if err != nil || len(report.Entries) != 0 {
		t.Fatalf("report = %+v, err = %v; want empty report", report, err)
	}
}

func TestCompactSandboxesListError(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{ListSandboxesErr: errors.New("boom")})

	if _, err := CompactSandboxes(
		context.Background(),
		"",
		msbSdk.DiskCompactionOptions{},
		&termio.Mock{},
	); err == nil {
		t.Fatal("expected list error")
	}
}

func TestCompactSandboxesSkipsFailures(t *testing.T) {
	failing := compactableHandle("failing-abc12345", nil)
	failing.CompactErr = errors.New("disk busy")
	gone := compactableHandle("gone-abc12345", nil)
	gone.CompactErr = &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	replaced := compactableHandle("replaced-abc12345", nil)
	replaced.CompactErr = &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"}
	remote := compactableHandle("remote-abc12345", nil)
	remote.CompactErr = &msbSdk.Error{Kind: msbSdk.ErrUnsupportedOperation, Message: "remote"}
	ok := compactableHandle("ok-abc12345", rootDiskResult(2, 1, 1))
	msb.WithMsbMock(t, &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{failing, gone, replaced, remote, ok}})

	ui := &termio.Mock{}
	report, err := CompactSandboxes(context.Background(), "", msbSdk.DiskCompactionOptions{}, ui)
	if err != nil {
		t.Fatalf("CompactSandboxes: %v", err)
	}
	if len(report.Entries) != 1 || report.Entries[0].Name != ok.Name_ {
		t.Fatalf("entries = %+v, want only ok", report.Entries)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "failing-abc12345") {
		t.Fatalf("WarnCalls = %v, want one warning for the failing sandbox", ui.WarnCalls)
	}
	verbose := strings.Join(ui.VerboseCalls, "\n")
	for _, name := range []string{"gone-abc12345", "replaced-abc12345", "remote-abc12345"} {
		if !strings.Contains(verbose, name) {
			t.Errorf("VerboseCalls = %v, want a skip note for %s", ui.VerboseCalls, name)
		}
	}
}

func TestRenderCompactReport(t *testing.T) {
	first := *rootDiskResult(5, 1, 2048)
	first.PauseUs = 1500
	report := CompactReport{
		DryRun: false,
		Entries: []CompactEntry{
			{Name: "a-abc12345", Slug: "a", Result: first},
			{Name: "b-abc12345", Slug: "b", Result: *rootDiskResult(3, 2, 1024)},
			{Name: "c-abc12345", Slug: "c", Result: msbSdk.DiskCompactionResult{PauseUs: 1}},
		},
	}
	ui := &termio.Mock{}
	RenderCompactReport(ui, report)

	if len(ui.OutCalls) != 1 || ui.OutCalls[0] != "Compacted 3 sandbox(es): 8 -> 3 layers, 3 KiB materialized" {
		t.Fatalf("OutCalls = %q", ui.OutCalls)
	}
	verbose := strings.Join(ui.VerboseCalls, "\n")
	for _, want := range []string{
		"  a-abc12345 (a): 5 -> 1 layers, 2 KiB materialized, paused 1.5ms",
		"    /: 5 -> 1 layers, 2 KiB materialized",
		"  b-abc12345 (b): 3 -> 2 layers, 1 KiB materialized\n",
		"  c-abc12345 (c): 0 -> 0 layers, 0 B materialized, paused 0.001ms",
	} {
		if !strings.Contains(verbose, want) {
			t.Errorf("VerboseCalls missing %q:\n%s", want, verbose)
		}
	}
}

func TestRenderCompactReportDryRun(t *testing.T) {
	ui := &termio.Mock{}
	RenderCompactReport(ui, CompactReport{DryRun: true})

	if len(ui.OutCalls) != 1 ||
		ui.OutCalls[0] != "dry-run: Would compact 0 sandbox(es): 0 -> 0 layers, 0 B materialized" {
		t.Fatalf("OutCalls = %q", ui.OutCalls)
	}
}

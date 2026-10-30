package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

const compactTestVMName = "agents-sandbox-vm-proj-1mjusbm3wikhb0"

func compactTestHandle() *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Name_:   compactTestVMName,
		Status_: msbSdk.SandboxStatusStopped,
		CompactResult: &msbSdk.DiskCompactionResult{
			InputLayers: 4, SelectedLayers: 4, OutputLayers: 1, MaterializedBytes: 2048,
		},
	}
}

func runCompactCommand(t *testing.T, args ...string) (*msb.MockSandboxHandle, []string, error) {
	t.Helper()
	vm := compactTestHandle()
	cmd, ui := setupCommandFixtures(t, append([]string{"sandbox", "compact"}, args...)...)
	msb.WithMsbMock(t, &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{vm}})
	err := cmd.Execute()
	return vm, ui.OutCalls, err
}

func TestSandboxCompactDefaultsCompactAllDisksWithoutPrompt(t *testing.T) {
	vm, out, err := runCompactCommand(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vm.CompactedOptions) != 1 || vm.CompactedOptions[0] != (msbSdk.DiskCompactionOptions{}) {
		t.Fatalf("CompactedOptions = %+v, want one zero-value call", vm.CompactedOptions)
	}
	if !slices.Contains(out, "Compacted 1 sandbox(es): 4 -> 1 layers, 2 KiB materialized") {
		t.Fatalf("OutCalls = %v", out)
	}
}

func TestSandboxCompactPassesFlags(t *testing.T) {
	vm, out, err := runCompactCommand(t, "--dry-run", "--root-only", "--layers", "3", compactTestVMName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := vm.CompactedOptions[0]
	if !got.DryRun || !got.RootDiskOnly || got.Disk != "" || got.Layers == nil || *got.Layers != 3 {
		t.Fatalf("options = %+v", got)
	}
	if !slices.Contains(out, "dry-run: Would compact 1 sandbox(es): 4 -> 1 layers, 2 KiB materialized") {
		t.Fatalf("OutCalls = %v", out)
	}
}

func TestSandboxCompactAcceptsMinimumLayers(t *testing.T) {
	vm, _, err := runCompactCommand(t, "--layers", "2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := vm.CompactedOptions[0].Layers; got == nil || *got != 2 {
		t.Fatalf("Layers = %v, want 2", got)
	}
}

func TestSandboxCompactDiskFlag(t *testing.T) {
	vm, _, err := runCompactCommand(t, "--disk", "/data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := vm.CompactedOptions[0]; got.Disk != "/data" || got.RootDiskOnly {
		t.Fatalf("options = %+v", got)
	}
}

func TestSandboxCompactRejectsInvalidFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"disk and root-only", []string{"--disk", "/", "--root-only"}, "none of the others can be"},
		{"layers below two", []string{"--layers", "1"}, "--layers must be at least 2"},
		{"bad format", []string{"--format", "yaml"}, "yaml"},
		{"unknown sandbox", []string{"missing"}, `no project sandbox named "missing"`},
		{"too many args", []string{"a", "b"}, "accepts at most 1 arg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vm, _, err := runCompactCommand(t, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
			if len(vm.CompactedOptions) != 0 {
				t.Fatal("no sandbox should be compacted on invalid input")
			}
		})
	}
}

func TestSandboxCompactJSON(t *testing.T) {
	_, out, err := runCompactCommand(t, "--format", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report struct {
		Sandboxes []struct {
			Name string `json:"name"`
		} `json:"sandboxes"`
	}
	if err := json.Unmarshal([]byte(out[0]), &report); err != nil {
		t.Fatalf("unmarshal %q: %v", out[0], err)
	}
	if len(report.Sandboxes) != 1 || report.Sandboxes[0].Name != compactTestVMName {
		t.Fatalf("report = %+v", report)
	}
}

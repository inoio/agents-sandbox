package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

var errBoom = errors.New("boom")

func runListCmdTest(t *testing.T, cmdArgs []string, mockSetup func(m *sandboxmsb.MockMsbClient), wantOut,
	wantInfo []string, wantErr bool, wantErrContains string) {
	t.Helper()
	mock := &sandboxmsb.MockMsbClient{}
	mockSetup(mock)
	cmd, ui := setupCommandFixtures(t, cmdArgs...)
	sandboxmsb.WithMsbMock(t, mock)

	if err := cmd.Execute(); err != nil {
		if !wantErr {
			t.Fatalf("unexpected error: %v", err)
		}
		if wantErrContains != "" && !strings.Contains(err.Error(), wantErrContains) {
			t.Errorf("error %q should contain %q", err.Error(), wantErrContains)
		}
		return
	}
	if wantErr {
		t.Error("expected error, got none")
		return
	}
	for _, want := range wantOut {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
	for _, want := range wantInfo {
		if !containsNormalized(ui.InfoCalls, want) {
			t.Errorf("InfoCalls missing %q; got: %v", want, ui.InfoCalls)
		}
	}
}

// containsNormalized reports whether any element of got matches want when both
// are whitespace-normalized. Column padding produced by the list renderers
// (e.g. %-50s) depends on identifier widths that change independently of
// test data, so alignment is not part of the assertion.
func containsNormalized(got []string, want string) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	wantN := norm(want)
	for _, g := range got {
		if norm(g) == wantN {
			return true
		}
	}
	return false
}

func TestListSandboxes(t *testing.T) {
	type testCase struct {
		name            string
		mockSetup       func(m *sandboxmsb.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}

	tests := []testCase{
		{
			name:      "empty (no sandboxes found)",
			mockSetup: func(_ *sandboxmsb.MockMsbClient) {},
			wantInfo:  []string{"No sandboxes found."},
		},
		{
			name: "one sandbox running",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Sandboxes = []sandboxmsb.SandboxHandle{
					&sandboxmsb.MockSandboxHandle{
						Name_:      "agents-sandbox-vm-abc123",
						Status_:    msb.SandboxStatusRunning,
						CreatedAt_: time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "agents-sandbox/runner:latest"},
					},
				}
			},
			wantOut: []string{
				"NAME IMAGE STATUS CREATED",
				"agents-sandbox-vm-abc123 agents-sandbox/runner:latest running 2026-08-17 10:30:00",
			},
		},
		{
			name: "multiple sandboxes",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Sandboxes = []sandboxmsb.SandboxHandle{
					&sandboxmsb.MockSandboxHandle{
						Name_:      "agents-sandbox-vm-alpha",
						Status_:    msb.SandboxStatusRunning,
						CreatedAt_: time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "agents-sandbox/runner:latest"},
					},
					&sandboxmsb.MockSandboxHandle{
						Name_:      "agents-sandbox-vm-beta",
						Status_:    msb.SandboxStatusStopped,
						CreatedAt_: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "agents-sandbox/runner:v1.0.0"},
					},
					&sandboxmsb.MockSandboxHandle{
						Name_:      "agents-sandbox-vm-gamma",
						Status_:    msb.SandboxStatusDraining,
						CreatedAt_: time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 15, 9, 15, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "agents-sandbox/runner:v2.0.0"},
					},
				}
			},
			wantOut: []string{
				"agents-sandbox-vm-alpha agents-sandbox/runner:latest running 2026-08-17 09:00:00",
				"agents-sandbox-vm-beta agents-sandbox/runner:v1.0.0 stopped 2026-08-16 12:00:00",
				"agents-sandbox-vm-gamma agents-sandbox/runner:v2.0.0 draining 2026-08-15 08:30:00",
			},
		},
		{
			name: "non-project VMs filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Sandboxes = []sandboxmsb.SandboxHandle{
					&sandboxmsb.MockSandboxHandle{Name_: "myvm-other-abc", Status_: msb.SandboxStatusRunning},
					&sandboxmsb.MockSandboxHandle{Name_: "legacy-vm-xyz", Status_: msb.SandboxStatusRunning},
					&sandboxmsb.MockSandboxHandle{
						Name_:      "agents-sandbox-vm-abc",
						Status_:    msb.SandboxStatusRunning,
						CreatedAt_: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "agents-sandbox/runner:latest"},
					},
				}
			},
			wantOut: []string{
				"agents-sandbox-vm-abc agents-sandbox/runner:latest running 2026-08-17 10:00:00",
			},
		},
		{
			name:            "list sandboxes error",
			mockSetup:       func(m *sandboxmsb.MockMsbClient) { m.ListSandboxesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list sandboxes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Parallel() - disabled due to shared global test hook
			runListCmdTest(
				t,
				[]string{cmdList},
				tt.mockSetup,
				tt.wantOut,
				tt.wantInfo,
				tt.wantErr,
				tt.wantErrContains,
			)
		})
	}
}

// projectSandbox builds a running project-VM mock handle with the given label map.
func projectSandbox(name string, labels map[string]string) *sandboxmsb.MockSandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      name,
		Status_:    msb.SandboxStatusRunning,
		CreatedAt_: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
		UpdatedAt_: time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
		Cfg:        &msb.SandboxConfig{Image: "agents-sandbox/runner:latest", Labels: labels},
	}
}

func TestListSandboxesLabelFilter(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-match",
			map[string]string{naming.LabelProject: "myslug"}),
		projectSandbox("agents-sandbox-vm-other",
			map[string]string{naming.LabelProject: "otherslug"}),
	}
	mock.ListSandboxesFn = func(_ context.Context, labels map[string]string) ([]sandboxmsb.SandboxHandle, error) {
		var filtered []sandboxmsb.SandboxHandle
		for _, h := range mock.Sandboxes {
			cfg, _ := h.Config()
			if labelsMatch(cfg.Labels, labels) {
				filtered = append(filtered, h)
			}
		}
		return filtered, nil
	}
	cmd, ui := setupCommandFixtures(t, cmdList, "--label", "org.agents-sandbox.project=myslug")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-match agents-sandbox/runner:latest running 2026-08-17 10:00:00",
	) {
		t.Errorf("matching sandbox row missing; got: %v", ui.OutCalls)
	}
	if containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-other agents-sandbox/runner:latest running 2026-08-17 10:00:00",
	) {
		t.Errorf("non-matching sandbox row should be absent; got: %v", ui.OutCalls)
	}
}

func labelsMatch(got, want map[string]string) bool {
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func TestListSandboxesLabelInvalid(t *testing.T) {
	runListCmdTest(t, []string{cmdList, "--label", "missing-equals"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "invalid label")
}

func TestListSandboxesLimit(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-alpha", nil),
		projectSandbox("agents-sandbox-vm-beta", nil),
	}
	cmd, ui := setupCommandFixtures(t, cmdList, "--limit", "1")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-alpha agents-sandbox/runner:latest running 2026-08-17 10:00:00",
	) {
		t.Errorf("alpha row missing; got: %v", ui.OutCalls)
	}
	if containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-beta agents-sandbox/runner:latest running 2026-08-17 10:00:00",
	) {
		t.Errorf("beta row should be absent when limited to 1; got: %v", ui.OutCalls)
	}
}

func TestListSandboxesLimitZeroMeansNoLimit(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-alpha", nil),
		projectSandbox("agents-sandbox-vm-beta", nil),
	}
	cmd, ui := setupCommandFixtures(t, cmdList, "--limit", "0")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"agents-sandbox-vm-alpha agents-sandbox/runner:latest running 2026-08-17 10:00:00",
		"agents-sandbox-vm-beta agents-sandbox/runner:latest running 2026-08-17 10:00:00",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing row %q with --limit 0 (should mean no limit); got: %v", want, ui.OutCalls)
		}
	}
}

func TestListSandboxesRunningOnly(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-running", nil),
		projectSandbox("agents-sandbox-vm-stopped", nil),
	}
	mock.Sandboxes[1].(*sandboxmsb.MockSandboxHandle).Status_ = msb.SandboxStatusStopped
	cmd, ui := setupCommandFixtures(t, cmdList, "--running")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-running agents-sandbox/runner:latest running 2026-08-17 10:00:00",
	) {
		t.Errorf("running sandbox row missing; got: %v", ui.OutCalls)
	}
	if containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-stopped agents-sandbox/runner:latest stopped 2026-08-17 10:00:00",
	) {
		t.Errorf("stopped sandbox row should be absent; got: %v", ui.OutCalls)
	}
}

func TestListSandboxesStoppedOnly(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-running", nil),
		projectSandbox("agents-sandbox-vm-stopped", nil),
	}
	mock.Sandboxes[1].(*sandboxmsb.MockSandboxHandle).Status_ = msb.SandboxStatusStopped
	cmd, ui := setupCommandFixtures(t, cmdList, "--stopped")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-stopped agents-sandbox/runner:latest stopped 2026-08-17 10:00:00",
	) {
		t.Errorf("stopped sandbox row missing; got: %v", ui.OutCalls)
	}
	if containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-running agents-sandbox/runner:latest running 2026-08-17 10:00:00",
	) {
		t.Errorf("running sandbox row should be absent; got: %v", ui.OutCalls)
	}
}

func TestListSandboxesBothFlagsRunningWins(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-running", nil),
		projectSandbox("agents-sandbox-vm-stopped", nil),
	}
	mock.Sandboxes[1].(*sandboxmsb.MockSandboxHandle).Status_ = msb.SandboxStatusStopped
	cmd, ui := setupCommandFixtures(t, cmdList, "--running", "--stopped")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-running agents-sandbox/runner:latest running 2026-08-17 10:00:00",
	) {
		t.Errorf("running sandbox row missing; got: %v", ui.OutCalls)
	}
	if containsNormalized(
		ui.OutCalls,
		"agents-sandbox-vm-stopped agents-sandbox/runner:latest stopped 2026-08-17 10:00:00",
	) {
		t.Errorf("stopped sandbox row should be absent when running wins; got: %v", ui.OutCalls)
	}
}

func TestListSandboxesQuietNames(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-alpha", nil),
		projectSandbox("agents-sandbox-vm-beta", nil),
	}
	cmd, ui := setupCommandFixtures(t, cmdList, "--names")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"agents-sandbox-vm-alpha", "agents-sandbox-vm-beta"} {
		if !containsNormalized(ui.PrintCalls, want) {
			t.Errorf("PrintCalls missing name %q; got: %v", want, ui.PrintCalls)
		}
	}
	if containsNormalized(ui.OutCalls, "NAME IMAGE STATUS CREATED") {
		t.Errorf("column header should be absent in quiet mode; got: %v", ui.OutCalls)
	}
	listCmd, _, err := cmd.Find([]string{cmdList})
	if err != nil {
		t.Fatalf("find list command: %v", err)
	}
	if flag := listCmd.Flags().Lookup(pFlagNames); flag == nil || flag.Deprecated == "" {
		t.Errorf("--names should be marked deprecated, got %+v", flag)
	}
}

func TestListSandboxesQuiet(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-alpha", nil),
		projectSandbox("agents-sandbox-vm-beta", nil),
	}
	cmd, ui := setupCommandFixtures(t, cmdList, "--quiet")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"agents-sandbox-vm-alpha", "agents-sandbox-vm-beta"} {
		if !containsNormalized(ui.PrintCalls, want) {
			t.Errorf("PrintCalls missing name %q; got: %v", want, ui.PrintCalls)
		}
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("quiet list should not write table output, got: %v", ui.OutCalls)
	}
}

// TestListSandboxesEnvQuiet proves an env/config-driven quiet (no --quiet flag)
// still selects identifiers-only output, since the handlers key off the
// effective UI quiet rather than the raw flag.
func TestListSandboxesEnvQuiet(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_QUIET", "true")
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-alpha", nil),
		projectSandbox("agents-sandbox-vm-beta", nil),
	}
	cmd, ui := setupCommandFixtures(t, cmdList)
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"agents-sandbox-vm-alpha", "agents-sandbox-vm-beta"} {
		if !containsNormalized(ui.PrintCalls, want) {
			t.Errorf("PrintCalls missing name %q; got: %v", want, ui.PrintCalls)
		}
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("env quiet list should not write table output, got: %v", ui.OutCalls)
	}
}

func TestListSandboxesFormatJSON(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		projectSandbox("agents-sandbox-vm-abc",
			map[string]string{naming.LabelProject: "myslug"}),
	}
	cmd, ui := setupCommandFixtures(t, cmdList, "--format", "json")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	var got []struct {
		Name    string            `json:"name"`
		Status  string            `json:"status"`
		Image   string            `json:"image"`
		Labels  map[string]string `json:"labels"`
		Created time.Time         `json:"created"`
		Updated time.Time         `json:"updated"`
	}
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sandbox in JSON, got %d: %q", len(got), joined)
	}
	s := got[0]
	if s.Name != "agents-sandbox-vm-abc" || s.Status != "running" ||
		s.Image != "agents-sandbox/runner:latest" || s.Labels[naming.LabelProject] != "myslug" {
		t.Errorf("unexpected JSON fields: %+v", s)
	}
	if s.Created.IsZero() || s.Updated.IsZero() {
		t.Errorf("created/updated should unmarshal as times, got %v / %v", s.Created, s.Updated)
	}
}

func TestListSandboxesFormatJSONAndNamesConflict(t *testing.T) {
	runListCmdTest(t, []string{cmdList, "--names", "--format", "json"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "mutually exclusive")
}

func TestListSandboxesFormatJSONQuietConflict(t *testing.T) {
	runListCmdTest(t, []string{cmdList, "--quiet", "--format", "json"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "mutually exclusive")
}

func TestListSandboxesFormatUnknown(t *testing.T) {
	runListCmdTest(t, []string{cmdList, "--format", "yaml"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "unsupported format")
}

func TestListImages(t *testing.T) {
	type testCase struct {
		name            string
		mockSetup       func(m *sandboxmsb.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}

	tests := []testCase{
		{
			name:      "empty (no images found)",
			mockSetup: func(_ *sandboxmsb.MockMsbClient) {},
			wantInfo:  []string{"No images found."},
		},
		{
			name: "one image",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Images = []sandboxmsb.ImageHandle{
					&sandboxmsb.MockImageHandle{
						Reference_:      "agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest",
						ManifestDigest_: "sha256-abc123def456",
						SizeBytes_:      int64PtrCLI(2 * 1024 * 1024 * 1024),
						CreatedAt_:      time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
					},
				}
			},
			wantOut: []string{
				"REFERENCE DIGEST SIZE CREATED",
				"agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest sha256-abc123def456 2 GiB 2026-08-17 10:42:36",
			},
		},
		{
			name: "multiple images",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Images = []sandboxmsb.ImageHandle{
					&sandboxmsb.MockImageHandle{
						Reference_:      "agents-sandbox/runner-alpha-1mjusbm3wikhb0:opencode-latest",
						ManifestDigest_: "sha256-aaa",
						SizeBytes_:      int64PtrCLI(864256000),
						CreatedAt_:      time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC),
					},
					&sandboxmsb.MockImageHandle{
						Reference_:      "agents-sandbox/runner-beta-1mjusbm3wikhb0:pi-latest",
						ManifestDigest_: "sha256-bbb",
						SizeBytes_:      int64PtrCLI(1024),
						CreatedAt_:      time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
					},
					&sandboxmsb.MockImageHandle{
						Reference_:      "agents-sandbox/runner-gamma-1mjusbm3wikhb0:opencode-latest",
						ManifestDigest_: "sha256-ccc",
					},
				}
			},
			wantOut: []string{
				"agents-sandbox/runner-alpha-1mjusbm3wikhb0:opencode-latest sha256-aaa 824.2 MiB 2026-08-17 09:00:00",
				"agents-sandbox/runner-beta-1mjusbm3wikhb0:pi-latest sha256-bbb 1 KiB 2026-08-16 12:00:00",
				"agents-sandbox/runner-gamma-1mjusbm3wikhb0:opencode-latest sha256-ccc unknown",
			},
		},
		{
			name: "non-opencode images filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Images = []sandboxmsb.ImageHandle{
					&sandboxmsb.MockImageHandle{
						Reference_:      "agents-sandbox/runner-xyz-1mjusbm3wikhb0:opencode-latest",
						ManifestDigest_: "sha256-abc123",
						SizeBytes_:      int64PtrCLI(512),
						CreatedAt_:      time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
					},
					&sandboxmsb.MockImageHandle{Reference_: "docker.io/some/img:latest", ManifestDigest_: "sha256-xxx"},
				}
			},
			wantOut: []string{
				"agents-sandbox/runner-xyz-1mjusbm3wikhb0:opencode-latest sha256-abc123 512 B 2026-08-17 10:00:00",
			},
		},
		{
			name:            "list images error",
			mockSetup:       func(m *sandboxmsb.MockMsbClient) { m.ListImagesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list images",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Parallel() - disabled due to shared global test hook
			runListCmdTest(
				t,
				[]string{cmdImage, cmdList},
				tt.mockSetup,
				tt.wantOut,
				tt.wantInfo,
				tt.wantErr,
				tt.wantErrContains,
			)
		})
	}
}

func TestListImagesQuiet(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Images = []sandboxmsb.ImageHandle{
		&sandboxmsb.MockImageHandle{
			Reference_:      "agents-sandbox/runner-aaa-1mjusbm3wikhb0:opencode-latest",
			ManifestDigest_: "sha256-aaa",
		},
		&sandboxmsb.MockImageHandle{
			Reference_:      "agents-sandbox/runner-bbb-1mjusbm3wikhb0:pi-latest",
			ManifestDigest_: "sha256-bbb",
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdImage, cmdList, "--quiet")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"agents-sandbox/runner-aaa-1mjusbm3wikhb0:opencode-latest",
		"agents-sandbox/runner-bbb-1mjusbm3wikhb0:pi-latest",
	} {
		if !containsNormalized(ui.PrintCalls, want) {
			t.Errorf("PrintCalls missing reference %q; got: %v", want, ui.PrintCalls)
		}
	}
	if containsNormalized(ui.OutCalls, "REFERENCE DIGEST SIZE CREATED") {
		t.Errorf("column header should be absent in quiet mode; got: %v", ui.OutCalls)
	}
}

func TestListImagesEnvQuiet(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_QUIET", "true")
	mock := &sandboxmsb.MockMsbClient{}
	mock.Images = []sandboxmsb.ImageHandle{
		&sandboxmsb.MockImageHandle{
			Reference_:      "agents-sandbox/runner-aaa-1mjusbm3wikhb0:opencode-latest",
			ManifestDigest_: "sha256-aaa",
		},
		&sandboxmsb.MockImageHandle{
			Reference_:      "agents-sandbox/runner-bbb-1mjusbm3wikhb0:pi-latest",
			ManifestDigest_: "sha256-bbb",
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdImage, cmdList)
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"agents-sandbox/runner-aaa-1mjusbm3wikhb0:opencode-latest",
		"agents-sandbox/runner-bbb-1mjusbm3wikhb0:pi-latest",
	} {
		if !containsNormalized(ui.PrintCalls, want) {
			t.Errorf("PrintCalls missing reference %q; got: %v", want, ui.PrintCalls)
		}
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("env quiet image list should not write table output, got: %v", ui.OutCalls)
	}
}

func TestListImagesFormatUnknown(t *testing.T) {
	runListCmdTest(t, []string{cmdImage, cmdList, "--format", "yaml"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "unsupported format")
}

func TestListImagesFormatJSON(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	reference := "agents-sandbox/runner-abc-1mjusbm3wikhb0:opencode-latest"
	mock.Images = []sandboxmsb.ImageHandle{
		&sandboxmsb.MockImageHandle{
			Reference_:      reference,
			ManifestDigest_: "sha256-abc",
			SizeBytes_:      int64PtrCLI(1024),
			CreatedAt_:      time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdImage, cmdList, "--format", "json")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	var got []struct {
		Reference string `json:"reference"`
		Digest    string `json:"digest"`
		Size      string `json:"size"`
		Created   string `json:"created"`
	}
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 image in JSON, got %d: %q", len(got), joined)
	}
	if got[0].Reference != reference || got[0].Digest != "sha256-abc" ||
		got[0].Size != "1 KiB" ||
		got[0].Created != "2026-08-17 10:00:00" {
		t.Errorf("unexpected JSON fields: %+v", got[0])
	}
}

func TestListImagesFormatJSONEmpty(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	cmd, ui := setupCommandFixtures(t, cmdImage, cmdList, "--format", "json")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.TrimSpace(strings.Join(ui.OutCalls, "\n"))
	if joined != "[]" {
		t.Errorf("empty image list JSON = %q, want %q", joined, "[]")
	}
}

func TestListVolumesQuiet(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Volumes = []sandboxmsb.VolumeHandle{
		&sandboxmsb.MockVolumeHandle{Name_: "agents-sandbox-home-aaa", Kind_: msb.VolumeKindDir},
		&sandboxmsb.MockVolumeHandle{Name_: "agents-sandbox-home-bbb", Kind_: msb.VolumeKindDir},
	}
	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdList, "--quiet")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"agents-sandbox-home-aaa", "agents-sandbox-home-bbb"} {
		if !containsNormalized(ui.PrintCalls, want) {
			t.Errorf("PrintCalls missing name %q; got: %v", want, ui.PrintCalls)
		}
	}
	if containsNormalized(ui.OutCalls, "NAME KIND SIZE CREATED") {
		t.Errorf("column header should be absent in quiet mode; got: %v", ui.OutCalls)
	}
}

func TestListVolumesFormatJSON(t *testing.T) {
	capacity := uint64(1024 * 1024 * 1024)
	mock := &sandboxmsb.MockMsbClient{}
	mock.Volumes = []sandboxmsb.VolumeHandle{
		&sandboxmsb.MockVolumeHandle{
			Name_:          "agents-sandbox-home-proj",
			Kind_:          msb.VolumeKindDisk,
			CapacityBytes_: &capacity,
			CreatedAt_:     time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdList, "--format", "json")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	var got []struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Size      string `json:"size"`
		CreatedAt string `json:"created"`
	}
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 volume in JSON, got %d: %q", len(got), joined)
	}
	if got[0].Name != "agents-sandbox-home-proj" || got[0].Kind != "disk" ||
		got[0].Size != "1 GiB" || got[0].CreatedAt != "2026-08-17 10:00:00" {
		t.Errorf("unexpected JSON fields: %+v", got[0])
	}
}

func TestListVolumesFormatJSONEmpty(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdList, "--format", "json")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.TrimSpace(strings.Join(ui.OutCalls, "\n"))
	if joined != "[]" {
		t.Errorf("empty volume list JSON = %q, want %q", joined, "[]")
	}
}

func TestListVolumesEnvQuiet(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_QUIET", "true")
	mock := &sandboxmsb.MockMsbClient{}
	mock.Volumes = []sandboxmsb.VolumeHandle{
		&sandboxmsb.MockVolumeHandle{Name_: "agents-sandbox-home-aaa", Kind_: msb.VolumeKindDir},
		&sandboxmsb.MockVolumeHandle{Name_: "agents-sandbox-home-bbb", Kind_: msb.VolumeKindDir},
	}
	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdList)
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"agents-sandbox-home-aaa", "agents-sandbox-home-bbb"} {
		if !containsNormalized(ui.PrintCalls, want) {
			t.Errorf("PrintCalls missing name %q; got: %v", want, ui.PrintCalls)
		}
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("env quiet volume list should not write table output, got: %v", ui.OutCalls)
	}
}

func TestListVolumesFormatUnknown(t *testing.T) {
	runListCmdTest(t, []string{cmdVolume, cmdList, "--format", "yaml"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "unsupported format")
}

func int64PtrCLI(n int64) *int64 { return new(n) }

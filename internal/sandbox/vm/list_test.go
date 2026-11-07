package vm

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

func TestFormatTime(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{name: "zero time", in: time.Time{}, want: ""},
		{
			name: "timestamp",
			in:   time.Date(2026, 8, 17, 10, 30, 45, 0, time.UTC),
			want: "2026-08-17 10:30:45",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatTime(tt.in); got != tt.want {
				t.Errorf("FormatTime(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestListSandboxesPopulatesInfo pins the fields a listed VM carries: name,
// status, image, labels, and its created/updated times both formatted and raw.
func TestListSandboxesPopulatesInfo(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC)
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:      "agents-sandbox-vm-abc",
			Status_:    msbSdk.SandboxStatusRunning,
			CreatedAt_: now,
			UpdatedAt_: now.Add(time.Hour),
			Cfg: &msbSdk.SandboxConfig{
				Image:  "agents-sandbox/runner:latest",
				Labels: map[string]string{naming.LabelProject: "proj"},
			},
		},
	}
	msb.WithMsbMock(t, mock)

	got, err := ListSandboxes(context.Background(), ListOption{})
	if err != nil {
		t.Fatalf("ListSandboxes() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListSandboxes() got %d infos, want 1", len(got))
	}
	info := got[0]
	if info.Name != "agents-sandbox-vm-abc" || info.Status != "running" ||
		info.Image != "agents-sandbox/runner:latest" {
		t.Errorf("name/status/image = %q/%q/%q", info.Name, info.Status, info.Image)
	}
	if info.Labels[naming.LabelProject] != "proj" {
		t.Errorf("Labels = %+v, want the project label", info.Labels)
	}
	if info.CreatedAt != "2026-08-17 10:30:00" || !info.CreatedAtRaw.Equal(now) ||
		!info.UpdatedAtRaw.Equal(now.Add(time.Hour)) {
		t.Errorf("times = %q, %v, %v", info.CreatedAt, info.CreatedAtRaw, info.UpdatedAtRaw)
	}
}

// TestListSandboxesFiltersAndLimits pins the status filter (running only or
// stopped only, running winning when both are set; starting, draining, created
// and crashed VMs match neither) and the limit, which truncates but never
// slices past the result.
func TestListSandboxesFiltersAndLimits(t *testing.T) {
	sandboxes := []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "running", Status_: msbSdk.SandboxStatusRunning},
		&msb.MockSandboxHandle{Name_: "stopped", Status_: msbSdk.SandboxStatusStopped},
		&msb.MockSandboxHandle{Name_: "starting", Status_: msbSdk.SandboxStatusStarting},
		&msb.MockSandboxHandle{Name_: "draining", Status_: msbSdk.SandboxStatusDraining},
		&msb.MockSandboxHandle{Name_: "created", Status_: msbSdk.SandboxStatusCreated},
		&msb.MockSandboxHandle{Name_: "crashed", Status_: msbSdk.SandboxStatusCrashed},
	}
	all := []string{"running", "stopped", "starting", "draining", "created", "crashed"}
	cases := []struct {
		name string
		opt  ListOption
		want []string
	}{
		{name: "no filter", want: all},
		{name: "running only", opt: ListOption{RunningOnly: true}, want: []string{"running"}},
		{name: "stopped only", opt: ListOption{StoppedOnly: true}, want: []string{"stopped"}},
		{name: "both", opt: ListOption{RunningOnly: true, StoppedOnly: true}, want: []string{"running"}},
		{name: "limit", opt: ListOption{Limit: intPtr(2)}, want: []string{"running", "stopped"}},
		{name: "limit above the count", opt: ListOption{Limit: intPtr(10)}, want: all},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msb.WithMsbMock(t, &msb.MockMsbClient{Sandboxes: sandboxes})

			infos, err := ListSandboxes(context.Background(), tc.opt)
			if err != nil {
				t.Fatalf("ListSandboxes: %v", err)
			}
			var got []string
			for _, info := range infos {
				got = append(got, info.Name)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("listed %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListSandboxesLabelsForwarded(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = nil
	var captured map[string]string
	mock.ListSandboxesFn = func(_ context.Context, labels map[string]string) ([]msb.SandboxHandle, error) {
		captured = labels
		return mock.Sandboxes, nil
	}
	msb.WithMsbMock(t, mock)

	if _, err := ListSandboxes(context.Background(), ListOption{Labels: map[string]string{"k": "v"}}); err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if captured == nil || captured["k"] != "v" {
		t.Errorf("expected labels forwarded to msb, got %+v", captured)
	}
}

// TestListSandboxesHidesTaskSandboxesBothSchemes pins that task sandboxes are
// left out whichever naming scheme or label marks them, and that the filter
// skips them rather than stopping: the project VM is listed after them.
func TestListSandboxesHidesTaskSandboxesBothSchemes(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:   "myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
			Status_: msbSdk.SandboxStatusRunning,
		},
		&msb.MockSandboxHandle{Name_: "agents-sandbox-task-x-1729", Status_: msbSdk.SandboxStatusRunning},
		&msb.MockSandboxHandle{
			Name_:   "labelled-prefill",
			Status_: msbSdk.SandboxStatusRunning,
			Cfg:     &msbSdk.SandboxConfig{Labels: map[string]string{naming.LabelTask: "1"}},
		},
		&msb.MockSandboxHandle{Name_: "myproj-1mjusbm3wikhb0-opencode", Status_: msbSdk.SandboxStatusRunning},
	}
	msb.WithMsbMock(t, mock)

	infos, err := ListSandboxes(context.Background(), ListOption{})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "myproj-1mjusbm3wikhb0-opencode" {
		t.Errorf("expected only the project VM, got %+v", infos)
	}
}

// TestListSandboxesWrapsListError pins the list-error wrap: the SDK's cause is
// preserved.
func TestListSandboxesWrapsListError(t *testing.T) {
	sentinel := errors.New("list boom")
	mock := &msb.MockMsbClient{ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
		return nil, sentinel
	}}
	msb.WithMsbMock(t, mock)

	_, err := ListSandboxes(context.Background(), ListOption{})
	if err == nil {
		t.Fatal("expected a list failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the list cause", err)
	}
}

func intPtr(n uint32) *uint32 { return new(n) }

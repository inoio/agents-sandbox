package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestInspectSandboxWrapsLookupError pins the sandbox-lookup wrap: a GetSandbox
// failure preserves its cause and names the requested VM.
func TestInspectSandboxWrapsLookupError(t *testing.T) {
	sentinel := errors.New("lookup boom")
	mock := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return nil, sentinel
	}}
	msb.WithMsbMock(t, mock)

	_, err := InspectSandbox(context.Background(), "agents-sandbox-vm-abc")
	if err == nil {
		t.Fatal("expected a lookup failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the lookup cause", err)
	}
	if !strings.Contains(err.Error(), "agents-sandbox-vm-abc") {
		t.Errorf("error = %v, want it to name the requested VM", err)
	}
}

// TestLessMountByGuestPathIsStrict pins the comparator as a strict weak ordering:
// it orders distinct paths by value and never orders a path before itself, so a
// "<=" boundary or a constant-true body is observable.
func TestLessMountByGuestPathIsStrict(t *testing.T) {
	a := MountDetail{GuestPath: "/a"}
	b := MountDetail{GuestPath: "/b"}

	if !lessMountByGuestPath(a, b) {
		t.Error("lessMountByGuestPath(/a, /b) = false, want true")
	}
	if lessMountByGuestPath(b, a) {
		t.Error("lessMountByGuestPath(/b, /a) = true, want false")
	}
	if lessMountByGuestPath(a, a) {
		t.Error("lessMountByGuestPath(/a, /a) = true, want false (irreflexive)")
	}
}

// TestMountDetailsSortsGuestPaths pins the end-to-end ordering across a map
// whose iteration order is randomized.
func TestMountDetailsSortsGuestPaths(t *testing.T) {
	volumes := map[string]msbSdk.MountConfig{
		"/z": {},
		"/a": {},
		"/m": {},
		"/b": {},
		"/y": {},
	}
	got := mountDetails(volumes)
	want := []string{"/a", "/b", "/m", "/y", "/z"}
	if len(got) != len(want) {
		t.Fatalf("mountDetails returned %d mounts, want %d", len(got), len(want))
	}
	for i, path := range want {
		if got[i].GuestPath != path {
			t.Fatalf("mountDetails[%d].GuestPath = %q, want %q (got %+v)", i, got[i].GuestPath, path, got)
		}
	}
}

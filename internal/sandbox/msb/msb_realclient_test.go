package msb

import (
	"context"
	"errors"
	"testing"
)

// TestGetFactory exercises the package-level Client factory, which the test
// hook normally overrides.
func TestGetFactory(t *testing.T) {
	c := realGetFactory()
	if _, ok := c.(*realMsbClient); !ok {
		t.Fatalf("Get() = %T, want *realMsbClient", c)
	}
}

// TestRealVolumeHandlePathUnknownValue covers the default branch of
// realVolumeHandle.Path when val holds a non-SDK type.
func TestRealVolumeHandlePathUnknownValue(t *testing.T) {
	v := &realVolumeHandle{val: "not a volume"}
	if got := v.Path(); got != "" {
		t.Errorf("Path() for unknown type = %q, want empty", got)
	}
}

// TestRealMsbClientCreateVolumeError covers the error branch of
// realMsbClient.CreateVolume (invalid empty name fails before touching a VM).
func TestRealMsbClientCreateVolumeError(t *testing.T) {
	c := &realMsbClient{}
	if _, err := c.CreateVolume(context.Background(), ""); err == nil {
		t.Error("CreateVolume() with empty name = nil error, want config error")
	}
}

// TestRealMsbClientListSandboxesWithLabels covers the label-filtering branch of
// realMsbClient.ListSandboxes.
func TestRealMsbClientListSandboxesWithLabels(t *testing.T) {
	c := &realMsbClient{}
	handles, err := c.ListSandboxes(context.Background(), map[string]string{"project": "demo"})
	if err != nil {
		t.Fatalf("ListSandboxes() with labels error = %v", err)
	}
	if handles == nil {
		t.Log("ListSandboxes() returned nil slice (no sandboxes present)")
	}
}

// TestMockMsbClientGetSandboxInternalBranches covers the getSandboxErr and
// gotSandbox override branches of MockMsbClient.GetSandbox that are not reached
// through the Set* helpers.
func TestMockMsbClientGetSandboxInternalBranches(t *testing.T) {
	ctx := context.Background()

	m := &MockMsbClient{getSandboxErr: errors.New("fail")}
	if _, err := m.GetSandbox(ctx, "x"); err == nil {
		t.Fatal("expected getSandboxErr")
	}

	h := &MockSandboxHandle{Name_: "s"}
	m2 := &MockMsbClient{gotSandbox: h}
	if got, err := m2.GetSandbox(ctx, "x"); err != nil || got != h {
		t.Fatalf("GetSandbox via gotSandbox = %v, %v; want %v", got, err, h)
	}
}

// TestMockMsbClientGetVolumeInternalBranches covers the GetVolumeErr and
// gotVolume override branches of MockMsbClient.GetVolume not reached through
// the Set* helpers.
func TestMockMsbClientGetVolumeInternalBranches(t *testing.T) {
	ctx := context.Background()

	m := &MockMsbClient{GetVolumeErr: errors.New("fail")}
	if _, err := m.GetVolume(ctx, "x"); err == nil {
		t.Fatal("expected GetVolumeErr")
	}

	v := &MockVolumeHandle{Name_: "vol"}
	m2 := &MockMsbClient{gotVolume: v}
	if got, err := m2.GetVolume(ctx, "x"); err != nil || got != v {
		t.Fatalf("GetVolume via gotVolume = %v, %v; want %v", got, err, v)
	}
}

// TestSetGetSandboxErrNil covers the else branch of SetGetSandboxErr clearing
// the override.
func TestSetGetSandboxErrNil(t *testing.T) {
	m := &MockMsbClient{}
	if got := m.SetGetSandboxErr(nil); got != m {
		t.Fatal("SetGetSandboxErr(nil) should return the receiver")
	}
	if m.GetSandboxFn != nil {
		t.Fatal("SetGetSandboxErr(nil) should clear GetSandboxFn")
	}
}

// TestMockSandboxHandleImageDefault covers the default branch of
// MockSandboxHandle.Image when no Image_ or Cfg is set.
func TestMockSandboxHandleImageDefault(t *testing.T) {
	h := &MockSandboxHandle{}
	if got := h.Image(); got != "" {
		t.Errorf("Image() = %q, want empty", got)
	}
}

// TestMockSandboxShellStreamFn covers the ShellStreamFn branch of
// MockSandbox.ShellStream.
func TestMockSandboxShellStreamFn(t *testing.T) {
	stub := &streamHandleStub{}
	m := &MockSandbox{
		ShellStreamFn: func(command string) (StreamHandle, error) {
			if command != "cmd" {
				t.Errorf("ShellStreamFn command = %q, want %q", command, "cmd")
			}
			return stub, nil
		},
	}
	got, err := m.ShellStream(context.Background(), "cmd")
	if err != nil {
		t.Fatalf("ShellStream() error = %v", err)
	}
	if got != stub {
		t.Fatal("ShellStream() did not return the ShellStreamFn handle")
	}
}

// TestEmptyStreamHandleClose covers emptyStreamHandle.Close.
func TestEmptyStreamHandleClose(t *testing.T) {
	var h StreamHandle = &emptyStreamHandle{}
	if err := h.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

// TestFailFastShellStreamPanics covers the otherwise-unreachable ShellStream
// method on the fail-fast client.
func TestFailFastShellStreamPanics(t *testing.T) {
	f := &failFastMsbClient{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic from fail-fast ShellStream")
		}
	}()
	_, _ = f.ShellStream(context.Background(), "cmd")
}

// TestRealVolumeHandlePathNilVal guards against a nil val.
func TestRealVolumeHandlePathNilVal(t *testing.T) {
	v := &realVolumeHandle{}
	if got := v.Path(); got != "" {
		t.Errorf("Path() with nil val = %q, want empty", got)
	}
}

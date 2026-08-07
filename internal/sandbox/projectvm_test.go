package sandbox

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestProjectVMName(t *testing.T) {
	got := projectVMName("myproj-aBc1234D")
	want := "opencode-msb-vm-myproj-aBc1234D"
	if got != want {
		t.Errorf("projectVMName(%q) = %q, want %q", "myproj-aBc1234D", got, want)
	}
}

func TestProjectVMNameTruncation(t *testing.T) {
	longSlug := "p-abcdef-very-long-slug-that-exceeds-the-128-byte-limit-and-then-some-more-padding"
	got := projectVMName(longSlug)
	if len(got) > maxSandboxNameLen {
		t.Errorf("expected name <= %d bytes, got %d", maxSandboxNameLen, len(got))
	}
	if len(got) < len(vmPrefix) {
		t.Errorf("name too short: %q", got)
	}
}

func TestBuildProjectVMEnvIncludesWorkspaces(t *testing.T) {
	envMap := map[string]string{
		"FOO": "bar",
	}
	buildProjectVMEnv(envMap, nil)
	if envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Errorf("expected OPENCODE_EXPERIMENTAL_WORKSPACES=true, got %q", envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"])
	}
	if envMap["PATH"] == "" {
		t.Error("expected PATH to have a fallback value")
	}
}

func TestBuildProjectVMEnvMergesImageEnvs(t *testing.T) {
	imageEnvs := map[string]string{
		"MY_KEY": "my_value",
		"PATH":   "/custom/path",
	}
	envMap := map[string]string{}
	buildProjectVMEnv(envMap, imageEnvs)
	if envMap["MY_KEY"] != "my_value" {
		t.Errorf("expected MY_KEY=my_value from image envs, got %q", envMap["MY_KEY"])
	}
	// Image env PATH should override the fallback PATH that buildProjectVMEnv sets.
	if envMap["PATH"] != "/custom/path" {
		t.Errorf("expected PATH=/custom/path from image envs, got %q", envMap["PATH"])
	}
	if envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Error("expected OPENCODE_EXPERIMENTAL_WORKSPACES to be set")
	}
}

func TestEnsureProjectVMCreatesWhenNotFound(t *testing.T) {
	notFoundErr := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	decision, err := decideVMAction(notFoundErr, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionCreate {
		t.Errorf("expected vmActionCreate, got %v", decision)
	}
}

func TestEnsureProjectVMConnectsWhenRunning(t *testing.T) {
	decision, err := decideVMAction(nil, msbSdk.SandboxStatusRunning)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionConnect {
		t.Errorf("expected vmActionConnect, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenStopped(t *testing.T) {
	decision, err := decideVMAction(nil, msbSdk.SandboxStatusStopped)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenCrashed(t *testing.T) {
	decision, err := decideVMAction(nil, msbSdk.SandboxStatusCrashed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart for crashed, got %v", decision)
	}
}

func TestCreateProjectVMCallsClientCreateSandbox(t *testing.T) {
	client := &MockMsbClient{}
	testUI := testutil.TermUIMock(t)
	ui := &testUI
	cfg := Config{
		UserStateDir:  t.TempDir(),
		UserConfigDir: t.TempDir(),
	}

	sb, created, err := createProjectVM(
		context.Background(),
		client,
		"opencode-msb-vm-test",
		"opencode-msb/runner-test:latest",
		"test-home-vol",
		t.TempDir(),
		RunOptions{Memory: "1G"},
		cfg,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("createProjectVM failed: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 created sandbox, got %d", len(client.CreatedSandboxes))
	}
	if client.CreatedSandboxes[0] != "opencode-msb-vm-test" {
		t.Errorf("expected sandbox name %q, got %q", "opencode-msb-vm-test", client.CreatedSandboxes[0])
	}
}

func TestStopProjectVMUsesClient(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI
	client := &MockMsbClient{}
	client.SetGotSandbox(&MockSandboxHandle{
		Name_:   "opencode-msb-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	oldGet := msb.Get
	msb.Get = func() MsbClient { return client }
	defer func() { msb.Get = oldGet }()

	// ProjectSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), false, false, ui); err != nil {
		t.Fatalf("StopProjectVM failed: %v", err)
	}
}

func TestEnsureProjectVM_CreatePath(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI

	notFoundErr := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, notFoundErr
	}
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	msb.WithMsbMock(t, client)

	cfg := Config{
		UserStateDir:  filepath.Join(t.TempDir(), "state"),
		UserConfigDir: t.TempDir(),
	}

	// ProjectSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := EnsureProjectVM(
		context.Background(),
		RunOptions{Memory: "1G", TmpSize: "512M"},
		cfg,
		"opencode-msb/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (create): %v", err)
	}
	if !created {
		t.Error("expected created=true on create path")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 created sandbox, got %d: %v", len(client.CreatedSandboxes), client.CreatedSandboxes)
	}
}

func TestEnsureProjectVM_ReconnectPath(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI

	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "opencode-msb-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return nil, errors.New("reconnect path must not create a sandbox")
	}
	msb.WithMsbMock(t, client)

	cfg := Config{
		UserStateDir:  filepath.Join(t.TempDir(), "state"),
		UserConfigDir: t.TempDir(),
	}

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := EnsureProjectVM(
		context.Background(),
		RunOptions{Memory: "1G", TmpSize: "512M"},
		cfg,
		"opencode-msb/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (reconnect): %v", err)
	}
	if created {
		t.Error("expected created=false on reconnect path")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 0 {
		t.Fatalf("expected no sandbox created on reconnect, got %v", client.CreatedSandboxes)
	}
}

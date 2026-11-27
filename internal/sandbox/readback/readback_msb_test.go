//go:build msb

package readback

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestReadBackOpenCode2 verifies the home read-back against a real VM: an
// opencode2 daemon stores a static API key through its credential API, the VM is
// stopped, and Read must find that key in the volume's SQLite store.
//
// It is host-only (KVM, a docker daemon, and network) and excluded from the
// default suite by the msb build tag, like the other real-runtime tests. Run it
// with:
//
//	CGO_ENABLED=1 go test ./internal/sandbox/readback/ -tags msb -run TestReadBackOpenCode2
//
// runtime.EnsureIsolatedRuntime installs the SDK-pinned runtime into an isolated
// microsandbox home, so the developer's real msb state is untouched. Setting
// AGENTS_SANDBOX_MSB_TEST_HOME reuses and keeps that home (and its image cache)
// between runs; the runner image is then reused from the cache, and only built
// and loaded when it is absent (the docker layer cache absorbs the build and
// EnsureLoaded skips the load when the cached content digest already matches).
const (
	// readbackAgentName is the harness under test.
	readbackAgentName = "opencode2"
	// readbackProjectSlug is the project identity the test seeds state under; its
	// runner-image slug shape is arbitrary here because the test reuses or builds
	// the image through the launcher.
	readbackProjectSlug = "readback-msb-test"
	// readbackRunnerSlug fixes the fallback-built runner image's identity. It has
	// the slug shape naming.IsRunnerImageRef accepts (ending in a 14-character
	// base36 hash), so a later run recognises the image as ours and reuses it
	// instead of reloading.
	readbackRunnerSlug = "readback-1mjusbm3wikhb0"
	// readbackCredentialProvider is the integration id the test's static key is
	// stored under; the probe reports it as Credential.Provider.
	readbackCredentialProvider = "openai"
	// readbackCredentialKey is the static key the daemon stores; Read must find
	// it in the credential row.
	readbackCredentialKey = "sk-readback-msb-test"
	// opencode2DaemonURL is the opencode2 serve daemon's loopback URL. The
	// credential call authenticates as the fixed user "opencode" with the
	// password agent/opencode2.go bakes into the runner image.
	opencode2DaemonURL = "http://127.0.0.1:4096"
	// daemonReadyTimeout bounds the wait for the serve daemon to answer.
	daemonReadyTimeout = 3 * time.Minute
)

// realMsbGet is the production microsandbox client factory, captured at package
// init before TestMain installs the fail-fast mock. The msb-tagged test drives
// the real SDK through it: realMsbClient is unexported in package msb, so a test
// outside that package cannot name it. Capturing here keeps the fail-fast net
// for every other test in this package.
var realMsbGet = sandboxmsb.Get

func TestReadBackOpenCode2(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	client := realMsbClientForTest(t)
	imageRef := ensureOpenCode2RunnerImage(t, ctx, client)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	homeVolume, err := client.CreateVolume(
		ctx,
		"readback-msb-test-home-"+suffix,
		sandboxmsb.VolumeSpec{Kind: sandboxmsb.VolumeKindDir},
	)
	if err != nil {
		t.Fatalf("create home volume: %v", err)
	}
	t.Cleanup(func() { removeVolumeForCleanup(t, client, homeVolume.Name()) })

	sandboxName := "readback-msb-test-" + suffix
	sandbox, err := client.CreateSandbox(ctx, sandboxName, sandboxmsb.SandboxSpec{
		Image: imageRef,
		Mounts: map[string]sandboxmsb.MountConfig{
			mounts.VMHomeDir: sandboxmsb.Mount.Named(homeVolume.Name(), sandboxmsb.MountOptions{}),
		},
		IdleTimeout: runtime.TestIdleTimeout,
		Detached:    true,
	})
	if err != nil {
		t.Fatalf("create opencode2 VM: %v", err)
	}
	t.Cleanup(func() { destroySandboxForCleanup(t, sandbox, sandboxName) })

	storeStaticKey(t, ctx, sandbox)

	// Stop the VM: the read-back reads the home volume at rest, not live.
	if err := sandbox.Destroy(ctx, sandboxmsb.LifecycleOptions{}); err != nil && !sandboxmsb.IsNotFound(err) {
		t.Fatalf("stop opencode2 VM: %v", err)
	}

	assertStaticCredentialReadBack(t, readBackHomeVolumePath(t, ctx, homeVolume.Name()))
}

// readBackHomeVolumePath seeds the test project's state with the VM's home
// volume name and resolves it through HomeVolumePath, exercising the
// state→volume→host-path chain against the real runtime rather than reading the
// volume handle's path directly.
func readBackHomeVolumePath(t *testing.T, ctx context.Context, volumeName string) string {
	t.Helper()
	// The volume was created through the real client, which resolves its
	// destination from MSB_HOME; the adapter's HomeVolumePath resolves the same
	// volume from configpaths.MSBHome(). Opt the mock into serving that home so
	// the lookup agrees with the creation, while state stays rooted in the mock
	// temp tree.
	configpathstest.WithMockConfigPathsMSBHomeFromEnv(t)
	key := state.Key{Slug: readbackProjectSlug, Agent: readbackAgentName}
	if err := state.WithHomeState(key, func(cur *state.HomeState, _ bool) (bool, error) {
		cur.HomeVolume = volumeName
		return true, nil
	}); err != nil {
		t.Fatalf("seed home state for %+v: %v", key, err)
	}
	path, err := HomeVolumePath(ctx, key)
	if err != nil {
		t.Fatalf("HomeVolumePath(%+v): %v", key, err)
	}
	return path
}

// realMsbClientForTest swaps the fail-fast client for the production one for the
// duration of the test, so the read-back test can create a real VM while every
// other test keeps the fail-fast mock.
func realMsbClientForTest(t *testing.T) sandboxmsb.Client {
	t.Helper()
	previous := sandboxmsb.Get
	sandboxmsb.Get = realMsbGet
	t.Cleanup(func() { sandboxmsb.Get = previous })
	return sandboxmsb.Get()
}

// removeVolumeForCleanup removes a test's home volume, logging rather than
// failing: cleanup must not mask the test's own result.
func removeVolumeForCleanup(t *testing.T, client sandboxmsb.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runtime.DestroyTimeout)
	defer cancel()
	if err := client.RemoveVolume(ctx, name); err != nil && !sandboxmsb.IsVolumeNotFound(err) {
		t.Logf("cleanup: remove home volume %s: %v", name, err)
	}
}

// destroySandboxForCleanup destroys a test's VM, tolerating the destroyed state
// the test itself may already have reached.
func destroySandboxForCleanup(t *testing.T, sandbox sandboxmsb.Sandbox, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runtime.DestroyTimeout)
	defer cancel()
	if err := sandbox.Destroy(ctx, sandboxmsb.LifecycleOptions{}); err != nil && !sandboxmsb.IsNotFound(err) {
		t.Logf("cleanup: destroy VM %s: %v", name, err)
	}
}

// ensureOpenCode2RunnerImage returns the reference of a cached opencode2 runner
// image, reusing it when present, and otherwise builds and loads one through the
// launcher's own image.Build. EnsureLoaded dedups by content digest, so the
// multi-GB docker-save transfer is skipped whenever the cached content matches.
func ensureOpenCode2RunnerImage(t *testing.T, ctx context.Context, client sandboxmsb.Client) string {
	t.Helper()
	if ref := cachedOpenCode2RunnerImage(ctx, client); ref != "" {
		t.Logf("reusing cached opencode2 runner image %s (MSB_HOME=%s)", ref, os.Getenv("MSB_HOME"))
		return ref
	}
	profile, ok := agent.Lookup(readbackAgentName)
	if !ok {
		t.Fatalf("agent.Lookup(%q) = not found", readbackAgentName)
	}
	t.Logf("no cached opencode2 runner image in MSB_HOME=%s; building one", os.Getenv("MSB_HOME"))
	info, err := image.Build(
		ctx,
		docker.RealClient(),
		client,
		configpaths.NewRealConfigPaths(),
		profile,
		readbackRunnerSlug,
		nil,
		image.BuildOptions{},
		readbackTestUI(),
	)
	if err != nil {
		t.Fatalf("build opencode2 runner image: %v", err)
	}
	return info.Ref
}

// cachedOpenCode2RunnerImage returns the newest cached runner image whose
// reference names the opencode2 agent, or "" when none is cached. The newest is
// chosen by creation time so a stale earlier image is not preferred.
func cachedOpenCode2RunnerImage(ctx context.Context, client sandboxmsb.Client) string {
	handles, err := client.ImageList(ctx)
	if err != nil {
		return ""
	}
	newest := ""
	var newestAt time.Time
	for _, handle := range handles {
		if naming.ParseImageTag(handle.Reference()).Agent != readbackAgentName {
			continue
		}
		if newest == "" || handle.CreatedAt().After(newestAt) {
			newest = handle.Reference()
			newestAt = handle.CreatedAt()
		}
	}
	return newest
}

// readbackTestUI is a quiet production UI for the image build: the build's
// progress is noise in a test, and every prompt path is stubbed by assumeYes.
func readbackTestUI() termio.UI {
	return termio.New(os.Stdin, io.Discard, io.Discard, false, termio.LevelInfo, true, true)
}

// storeStaticKey starts the opencode2 serve daemon in sandbox, waits for it, and
// stores a static API key through the daemon's credential API.
func storeStaticKey(t *testing.T, ctx context.Context, sandbox sandboxmsb.Sandbox) {
	t.Helper()
	profile, ok := agent.Lookup(readbackAgentName)
	if !ok {
		t.Fatalf("agent.Lookup(%q) = not found", readbackAgentName)
	}
	daemon, ok := agent.AsDaemonProvider(profile)
	if !ok {
		t.Fatalf("%s does not implement agent.DaemonProvider", readbackAgentName)
	}

	command := daemon.DaemonCommand(false)
	job, err := sandbox.ExecDetached(ctx, command.Binary, command.Args, sandboxmsb.JobSpec{})
	if err != nil {
		t.Fatalf("start %s daemon: %v", readbackAgentName, err)
	}
	t.Cleanup(func() { _ = job.Close() })

	awaitDaemonHealthy(t, ctx, sandbox, daemon)
	postStaticKey(t, ctx, sandbox)
}

// awaitDaemonHealthy polls the daemon's health command until it reports ready.
func awaitDaemonHealthy(
	t *testing.T,
	ctx context.Context,
	sandbox sandboxmsb.Sandbox,
	daemon agent.DaemonProvider,
) {
	t.Helper()
	deadline := time.Now().Add(daemonReadyTimeout)
	for {
		result, err := sandbox.Shell(ctx, daemon.DaemonHealthCmd(), sandboxmsb.ExecRequest{})
		if err == nil {
			if healthy, _ := daemon.DaemonHealthParse(result.Stdout()); healthy {
				return
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%s daemon did not become ready within %s", readbackAgentName, daemonReadyTimeout)
		}
		time.Sleep(2 * time.Second)
	}
}

// postStaticKey stores readbackCredentialKey under readbackCredentialProvider via
// the daemon's credential API. The JSON body carries no single quote, so
// single-quoting it for the shell is safe.
func postStaticKey(t *testing.T, ctx context.Context, sandbox sandboxmsb.Sandbox) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"integrationID": readbackCredentialProvider,
		"label":         "OpenAI",
		"activate":      true,
		"value":         map[string]string{"type": "key", "key": readbackCredentialKey},
	})
	if err != nil {
		t.Fatalf("encode credential request: %v", err)
	}
	command := fmt.Sprintf(
		`curl -fsS -m 30 -u "opencode:${OPENCODE_SERVER_PASSWORD:-opencode}" `+
			`-H 'Content-Type: application/json' -X POST --data '%s' %s/api/credential`,
		body,
		opencode2DaemonURL,
	)
	result, err := sandbox.Shell(ctx, command, sandboxmsb.ExecRequest{})
	if err != nil {
		t.Fatalf("store static credential: %v", err)
	}
	if !result.Success() {
		t.Fatalf("store static credential: exit %d: %s%s", result.ExitCode(), result.Stdout(), result.Stderr())
	}
}

// assertStaticCredentialReadBack asserts Read finds exactly the stored static
// key in the stopped home volume, and that the volume carries the opencode2
// credential probe's manifest entry.
func assertStaticCredentialReadBack(t *testing.T, home string) {
	t.Helper()
	if home == "" {
		t.Fatal("home volume path is empty")
	}

	readBack, err := Read(home, readbackAgentName, Manifest{})
	if err != nil {
		t.Fatalf("Read(%s, %q): %v", home, readbackAgentName, err)
	}
	if len(readBack.Unreadable) != 0 {
		t.Fatalf(
			"read-back could not read %v; the credential store is undetermined, not empty",
			readBack.Unreadable,
		)
	}
	var found []Credential
	for _, credential := range readBack.Static {
		if credential.Provider == readbackCredentialProvider {
			found = append(found, credential)
		}
	}
	if len(found) != 1 {
		t.Fatalf(
			"Read found %d static credentials for %q, want exactly 1: %+v",
			len(found),
			readbackCredentialProvider,
			readBack,
		)
	}
	if found[0].Kind != CredentialStatic {
		t.Errorf("credential kind = %v, want CredentialStatic", found[0].Kind)
	}
	if got := found[0].Value; got != readbackCredentialKey {
		t.Errorf("credential value = %q, want %q", got, readbackCredentialKey)
	}
	if want := readbackAgentName + ":opencode2-sqlite"; found[0].Source != want {
		t.Errorf("credential source = %q, want %q", found[0].Source, want)
	}

	manifest, err := TakeManifest(home, readbackAgentName)
	if err != nil {
		t.Fatalf("TakeManifest(%s, %q): %v", home, readbackAgentName, err)
	}
	probeKey := readbackAgentName + ":opencode2-sqlite"
	for _, entry := range manifest.Entries {
		if entry.Key == probeKey {
			return
		}
	}
	t.Errorf("manifest has no %q probe entry: %+v", probeKey, manifest)
}

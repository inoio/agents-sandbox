//go:build msb

package journeys

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// stubSpikePrompt is the one message the spike sends; a completed model turn is
// not required, only that the agent issues a request to the stub host.
const stubSpikePrompt = "hello"

// TestStubProviderTurnReachesTheProxy is the Task 13 host-gated spike: it drives
// a real `run --learn` on opencode2 against the host-side stub provider and
// asserts the saved observation names the stub host, proving the agent's turn
// traverses the host-owned terminating proxy. It is a human accept on a KVM
// host and skips (never fails) without one; `make compile-all` compiles it.
//
// The advertised route defaults to the documented placeholder. When the default
// route cannot reach the proxy, or the proxy cannot resolve it, a host accept
// pins a working route with AGENTS_SANDBOX_JOURNEYS_STUB_ORIGIN and reruns. The
// assertion is only that a destination for the stub host was *observed* — a
// denied dial is still observed — so it passes as long as the turn reaches the
// proxy at all.
func TestStubProviderTurnReachesTheProxy(t *testing.T) {
	driver.Preflight(t)
	root, binary := driver.Prepare(t)
	driver.PreflightDocker(t, root)
	driver.SweepSandboxes(t, root, binary, "stub-spike")
	t.Cleanup(func() { driver.SweepSandboxes(t, root, binary, "cleanup") })
	if !driver.SmokeRootReused() {
		t.Cleanup(func() { driver.CleanupRunnerImages(t, root) })
	}
	driver.PrebuildImages(t, root)

	const agentName = "opencode2"
	spec, ok := driver.Specs[agentName]
	if !ok {
		t.Fatalf("no smoke spec for agent %s", agentName)
	}
	// The stub's ProviderConfig is seeded as a managed launcher snippet, so an
	// explicit --learn is used: the first-run menu is out of scope for the spike.
	ctx := &Context{
		TB:     t,
		Root:   root,
		Binary: binary,
		Spec:   spec,
		Agent:  agentName,
		Home:   t.TempDir(),
	}
	steps := []Step{
		StepFreshHome(),
		StepSeedStub(),
		StepLaunch("run", "--learn"),
		StepWaitReady(),
		StepSendPrompt(stubSpikePrompt),
		StepQuit(),
	}
	for _, step := range steps {
		if err := step.Run(ctx); err != nil {
			t.Fatalf("step %q: %v\n%s", step.Name, err, spikeOutput(ctx))
		}
	}
	// Read the artefact while the exit prompt holds the launcher open: the save
	// happens before the prompt, and the after-run deletes the artefact once it
	// is answered.
	assertStubDestinationObserved(t, ctx)
	if err := StepChooseExit(exitSaveAllKey).Run(ctx); err != nil {
		t.Fatalf("step %q: %v\n%s", "choose exit", err, spikeOutput(ctx))
	}
}

// assertStubDestinationObserved fails unless a saved learn artefact under the
// journey's isolated state dir names the stub provider's advertised host. It
// reports every host it did see, so a route that reaches the proxy under a
// different name is diagnosable from the failure.
func assertStubDestinationObserved(t *testing.T, c *Context) {
	t.Helper()

	provider := c.Stub
	if provider == nil {
		t.Fatal("no stub provider on the context")
	}
	host := advertisedStubHost(t, provider.Addr())
	learnDir := filepath.Join(c.Home, isolatedStateDir, launcherConfigDirName, "learn")
	artefacts, err := filepath.Glob(filepath.Join(learnDir, "*", "*.json"))
	if err != nil {
		t.Fatalf("glob learn artefacts under %s: %v", learnDir, err)
	}
	if len(artefacts) == 0 {
		t.Fatalf("the run saved no learn artefact under %s:\n%s", learnDir, spikeOutput(c))
	}
	var observed []string
	for _, path := range artefacts {
		observation, err := channel.Load(path)
		if err != nil {
			t.Fatalf("load observation %s: %v", path, err)
		}
		for _, destination := range observation.Destinations {
			observed = append(observed, destination.Host)
			if destination.Host == host {
				return
			}
		}
	}
	t.Fatalf(
		"the saved observation names no destination for %q (saw %v); pin a working route with %s and rerun:\n%s",
		host, observed, stubOriginEnv, spikeOutput(c),
	)
}

// advertisedStubHost returns the host part of the stub's advertised address.
func advertisedStubHost(t *testing.T, addr string) string {
	t.Helper()

	parsed, err := url.Parse(addr)
	if err != nil || parsed.Hostname() == "" {
		t.Fatalf("stub Addr %q is not a URL with a host", addr)
	}
	return parsed.Hostname()
}

// spikeOutput returns the session's terminal output, or "" before StepLaunch set
// a session, so a step failure can always be reported.
func spikeOutput(c *Context) string {
	if c.Session == nil {
		return ""
	}
	return driver.StripANSI(c.Session.Output())
}

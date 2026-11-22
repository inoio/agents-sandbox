//go:build msb

package startup

import (
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestHarnessSmoke starts every registered harness the way a user does
// (`agents-sandbox run --agent <name>`) inside a real microsandbox VM and
// asserts it reaches its full-screen TUI, exits 0 on its quit key, and never
// reports a daemon timeout. A sibling subtest runs the docker-storage smoke
// (runDockerStorageDriverSmoke) in the same suite. It requires KVM, a docker
// daemon, and network; the `msb` build tag keeps it out of the default test run.
//
// The checks run in order so an environment that cannot run the suite skips
// before paying for it: the platform/`/dev/kvm` preflight first (no build), then
// the shared root and binary, then the isolated-env docker preflight, then the
// image prebuild (the first docker-dependent step), then the harnesses.
//
// The per-harness subtests run in parallel, bounded by a semaphore sized to the
// host's free memory (driver.HarnessConcurrency); each gets its own prepared HOME
// tree (driver.HomeBase) while sharing the root's microsandbox home, so the image
// cache stays common.
//
//nolint:tparallel // top-level tests share the suite root and docker/VM state and must not overlap
func TestHarnessSmoke(t *testing.T) {
	driver.Preflight(t)
	root, binary := driver.Prepare(t)
	driver.PreflightDocker(t, root)
	// Register cleanup after the docker preflight so a skip touches nothing, but
	// before the prebuild so a prebuild failure cannot leak the root. t.Cleanup,
	// not defer: it must run after the parallel subtests, which resume only once
	// this function returns.
	t.Cleanup(func() { driver.CleanupSandboxes(t, root, binary) })
	if !driver.SmokeRootReused() {
		t.Cleanup(func() { driver.CleanupRunnerImages(t, root) })
	}
	driver.PrebuildImages(t, root)

	slots := make(chan struct{}, driver.HarnessConcurrency())
	t.Logf("running up to %d smoke subtests in parallel", cap(slots))
	for _, name := range agent.Names() {
		spec, ok := driver.Specs[name]
		if !ok {
			t.Fatalf("harness %q has no smoke-test spec", name)
		}
		network := spec.Network
		if network == "" {
			network = driver.NetworkNone
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			slots <- struct{}{}
			defer func() { <-slots }()
			homeBase := driver.HomeBase(root, name)
			if err := driver.PrepareHome(homeBase); err != nil {
				t.Fatalf("prepare %s home: %v", name, err)
			}
			if err := driver.SeedHomeFiles(homeBase, spec.SeedHomeFiles); err != nil {
				t.Fatalf("seed %s home files: %v", name, err)
			}
			if err := driver.SeedLauncherConfig(homeBase, spec.SeedHomeManifest); err != nil {
				t.Fatalf("seed %s launcher config: %v", name, err)
			}
			profile, _ := agent.Lookup(name)
			if err := driver.SeedAgentSnippets(homeBase, profile.ConfigDirName(), spec.SeedAgentSnippets); err != nil {
				t.Fatalf("seed %s agent snippets: %v", name, err)
			}
			ses := driver.MustStartUnderPTY(
				t,
				driver.ProjectDir(root, name),
				driver.IsolatedEnv(root, homeBase, os.Environ()),
				append(
					[]string{binary},
					driver.WithVMCPUs(driver.HarnessRunArgs(name, network), driver.SmokeVMCPUs())...)...,
			)
			if err := driver.WaitReady(ses, spec); err != nil {
				t.Fatalf(
					"harness %s did not reach its TUI: %v\n--- output ---\n%s",
					name,
					err,
					driver.StripANSI(ses.Output()),
				)
			}
			code, err := driver.Quit(ses, spec)
			if err != nil {
				t.Fatalf("harness %s did not exit: %v\n--- output ---\n%s",
					name, err, driver.StripANSI(ses.Output()))
			}
			if code != 0 {
				t.Fatalf("harness %s exited %d, want 0\n--- output ---\n%s", name, code, driver.StripANSI(ses.Output()))
			}
			if driver.SawDaemonTimeout(ses.Output()) {
				t.Fatalf("harness %s reported a daemon timeout:\n%s", name, driver.StripANSI(ses.Output()))
			}
		})
	}

	// The docker-storage smoke is not a harness (the table's key set must equal
	// agent.Names()), so it runs as a sibling subtest: it shares the suite root,
	// home, prebuild, and cleanup, and overlaps the harnesses through the same
	// slot pool.
	t.Run("docker-storage-driver", func(t *testing.T) {
		t.Parallel()
		slots <- struct{}{}
		defer func() { <-slots }()
		runDockerStorageDriverSmoke(t, root, binary)
	})
}

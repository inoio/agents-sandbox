//go:build msb

package journeys

import (
	"testing"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestHarnessJourneys drives each journey in the table against a real
// microsandbox VM, one journey at a time. It requires /dev/kvm, a docker daemon,
// and network; the `msb` build tag keeps it out of the default test run, and
// driver.Preflight skips (never fails) on a host that cannot run it.
//
// The suite builds the launcher and prebuilds every runner image once, exactly
// as the startup smoke does, so a journey's budget only has to absorb its VM
// start. A step's error aborts its journey and names the failing step, so a
// scenario reads top-to-bottom and a failure points at one line.
func TestHarnessJourneys(t *testing.T) {
	driver.Preflight(t)
	if len(Journeys) == 0 {
		// Until Phase 2 adds journeys, building the launcher and prebuilding
		// every runner image would be a multi-minute no-op. Skip before either.
		t.Skip("no journeys defined")
	}
	root, binary := driver.Prepare(t)
	driver.PreflightDocker(t, root)
	// Clear a reused root's leftovers before anything starts, and sweep again at
	// teardown so a failed journey cannot leak a VM (see the startup suite).
	driver.SweepSandboxes(t, root, binary, "journeys")
	// Register cleanup after the docker preflight so a skip touches nothing, but
	// before the prebuild so a prebuild failure cannot leak the root.
	t.Cleanup(func() { driver.SweepSandboxes(t, root, binary, "cleanup") })
	if !driver.SmokeRootReused() {
		t.Cleanup(func() { driver.CleanupRunnerImages(t, root) })
	}
	driver.PrebuildImages(t, root)

	for _, journey := range Journeys {
		t.Run(journey.Name, func(t *testing.T) {
			spec, ok := driver.Specs[journey.Agent]
			if !ok {
				t.Fatalf("journey %q names agent %q with no smoke spec", journey.Name, journey.Agent)
			}
			ctx := &Context{
				TB:      t,
				Root:    root,
				Binary:  binary,
				Spec:    spec,
				Agent:   journey.Agent,
				Home:    t.TempDir(),
				Session: nil,
			}
			for _, step := range journey.Steps {
				if err := step.Run(ctx); err != nil {
					t.Fatalf("journey %q step %q: %v", journey.Name, step.Name, err)
				}
			}
		})
	}
}

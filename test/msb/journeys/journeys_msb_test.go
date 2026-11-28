//go:build msb

package journeys

import (
	"os"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// The make target's JOURNEY and HARNESS selection, which also narrows the
// prebuild to the images the selected cells drive.
const (
	journeySelectionEnv = "AGENTS_SANDBOX_JOURNEYS_JOURNEY"
	harnessSelectionEnv = "AGENTS_SANDBOX_JOURNEYS_HARNESS"
)

// TestHarnessJourneys drives each journey in the table against real
// microsandbox VMs: the journeys one after another, and a journey's harnesses
// in parallel, as many at once as driver.HarnessConcurrency allows (the
// startup smoke's limit), except for journeys marked SerialHarnesses to avoid
// shared host callback ports. Each harness has its own project, so parallel cells
// never share a VM, and the journeys' order keeps a harness's shared project
// to one journey at a time. It requires /dev/kvm, a docker daemon,
// and network; the `msb` build tag keeps it out of the default test run, and
// driver.Preflight skips (never fails) on a host that cannot run it.
//
// The suite builds the launcher and prebuilds the runner images of the
// selected cells once, so a journey's budget only has to absorb its VM start. A step's error aborts its journey and names the failing step, so a
// scenario reads top-to-bottom and a failure points at one line.
//
// Each journey runs once per harness it lists, as TestHarnessJourneys/<journey>/
// <harness>. A journey drives the harness's shared project
// (driver.ProjectDir(root, harness)) unless it starts with StepOwnProject, so
// journeys on the shared project share one VM and home volume and the sweep only
// runs at suite start and teardown. Per-journey state a step asserts must
// therefore live in that journey's isolated HOME (a fresh t.TempDir per run, via
// driver.IsolatedEnv), never in the shared volume or project dir.
func TestHarnessJourneys(t *testing.T) {
	driver.Preflight(t)
	root, binary := driver.Prepare(t)
	driver.PreflightDocker(t, root)
	// Clear a reused root's leftovers before anything starts, and sweep again at
	// teardown so a failed journey cannot leak a VM (see the startup suite).
	driver.SweepSandboxes(t, root, binary, "journeys")
	// Register cleanup after the docker preflight so a skip touches nothing, but
	// before the prebuild so a prebuild failure cannot leak the root.
	t.Cleanup(func() { driver.SweepSandboxes(t, root, binary, "cleanup") })
	selection := journeySelection(t)
	if !driver.SmokeRootReused() {
		t.Cleanup(func() { driver.CleanupRunnerImagesFor(t, root, selection) })
	}
	driver.PrebuildImagesFor(t, root, selection)

	slots := make(chan struct{}, driver.HarnessConcurrency())
	for _, journey := range Journeys {
		t.Run(journey.Name, func(t *testing.T) {
			for _, harness := range journey.Harnesses {
				t.Run(harness, func(t *testing.T) {
					runHarnessCell(journey, t.Parallel, slots, func() {
						runJourney(t, journeyContext(t, root, binary, journey, harness), journey, time.Now)
					})
				})
			}
		})
	}
}

// journeyContext builds the context one (journey, harness) run starts from: the
// journey's spec for the harness (journeySpec), its journey adapter, and a
// fresh HOME.
func journeyContext(t *testing.T, root, binary string, journey Journey, harness string) *Context {
	t.Helper()
	spec, ok := driver.Specs[harness]
	if !ok {
		t.Fatalf("journey %q names harness %q with no smoke spec", journey.Name, harness)
	}
	adapter, ok := Harnesses[harness]
	if !ok {
		t.Fatalf("journey %q names harness %q with no journey adapter", journey.Name, harness)
	}
	return &Context{
		TB: t, Root: root, Binary: binary, Spec: journeySpec(spec, journey, adapter), Agent: harness,
		Harness: adapter, Home: t.TempDir(),
	}
}

// journeySelection is the images the make target's JOURNEY/HARNESS selection
// drives. A selection
// that names no cell fails, so a typo cannot pass as an empty green run.
func journeySelection(t *testing.T) []driver.ProjectAgent {
	t.Helper()
	//testenv:allow the make target passes the JOURNEY/HARNESS selection through the environment
	journey, harness := os.Getenv(journeySelectionEnv), os.Getenv(harnessSelectionEnv)
	selection := selectedProjectAgents(Journeys, journey, harness)
	if len(selection) == 0 && (journey != "" || harness != "") {
		t.Fatalf("JOURNEY=%q HARNESS=%q selects no journey cell", journey, harness)
	}
	return selection
}

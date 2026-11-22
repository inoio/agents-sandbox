// Package journeys holds the harness-journey acceptance suite: a second
// `msb`-tagged suite that drives the built launcher through full user journeys
// (v1: learn mode on opencode2) inside real microsandbox VMs. The journey model
// and the step vocabulary are untagged so the table-completeness test runs in
// the default `make test`; only the host runner carries the `msb` build tag. See
// specs/testing/msb-journeys.md.
package journeys

import "github.com/inoio/agents-sandbox/test/msb/driver"

// Context is the mutable state one journey's steps share. The runner builds it
// once per journey and passes the same pointer to every step in order.
//
//exhaustruct:ignore
type Context struct {
	TB     driver.TB   // the *testing.T the runner passed in
	Root   string      // the suite root from driver.Prepare
	Binary string      // the built launcher
	Spec   driver.Spec // driver.Specs[agent]
	Agent  string      // the journey's registered agent name
	Home   string      // this journey's isolated HOME base (HOME and the XDG dirs)
	// Session is set by StepLaunch and driven by the later steps.
	Session *driver.Session
	// Stub is set by StepSeedStub: the host-side destination the journey's
	// agent sends to, kept so a later step or a host accept can inspect it.
	Stub StubProvider
}

// Step is one named action in a journey. A step's Run returns an error; the
// runner aborts the journey there and reports Name, so a failure names the step.
type Step struct {
	Name string
	Run  func(*Context) error
}

// Journey is one named scenario driven against one registered agent, composed of
// steps drawn from the shared vocabulary in steps.go. No journey inlines a step.
//
// Network overrides the agent's spec network profile for this journey when set
// (driver.Spec.NetworkOrDefault otherwise). The attribution journey sets it to a
// profile that admits the stub, so the run's baseline is broad enough for the
// analyser to actually narrow it and the network-not-widened assertion to have a
// posture to check.
//
//exhaustruct:ignore
type Journey struct {
	Name    string
	Agent   string
	Network string
	Steps   []Step
}

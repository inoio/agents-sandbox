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
	// Project is the journey's own project dir, set by StepOwnProject; empty
	// means the agent's shared project (see ProjectDir).
	Project string
	// Harness is the agent's journey adapter (Harnesses[Agent]).
	Harness Harness
	// Session is set by StepLaunch and driven by the later steps.
	Session *driver.Session
	// Stub is set by StepSeedStub: the host-side destination the journey's
	// agent sends to, kept so a later step or a host accept can inspect it.
	Stub StubProvider
}

// ProjectDir is the project dir the journey runs in: its own project when
// StepOwnProject set one, else the agent's shared project.
func (c *Context) ProjectDir() string {
	if c.Project != "" {
		return c.Project
	}
	return driver.ProjectDir(c.Root, c.Agent)
}

// Keyword is a step's Gherkin keyword: it only shapes how the scenario reads.
type Keyword string

// The Gherkin keywords a journey's steps carry.
const (
	KeywordGiven Keyword = "Given"
	KeywordWhen  Keyword = "When"
	KeywordThen  Keyword = "Then"
	KeywordAnd   Keyword = "And"
)

// StepKind decides what a failing step does to the rest of its journey.
type StepKind int

const (
	// Action is a step later steps depend on: its failure ends the journey.
	Action StepKind = iota
	// Check is an assertion nothing later depends on: its failure is recorded
	// and the journey continues, so one run reports every broken check.
	Check
)

// Step is one named step in a journey. Name is the sentence after the keyword,
// so Text reads as a Gherkin line and a failure names the step in that form.
//
//exhaustruct:ignore
type Step struct {
	Keyword Keyword
	Kind    StepKind
	Name    string
	Run     func(*Context) error
}

// Text is the step's Gherkin line.
func (s Step) Text() string {
	return string(s.Keyword) + " " + s.Name
}

// Given sets the step's keyword to Given; When, Then, and And are its siblings
// for the other keywords. A journey literal wraps each step in one of them.
func Given(step Step) Step { return withKeyword(step, KeywordGiven) }

// When sets the step's keyword to When.
func When(step Step) Step { return withKeyword(step, KeywordWhen) }

// Then sets the step's keyword to Then.
func Then(step Step) Step { return withKeyword(step, KeywordThen) }

// And sets the step's keyword to And.
func And(step Step) Step { return withKeyword(step, KeywordAnd) }

func withKeyword(step Step, keyword Keyword) Step {
	step.Keyword = keyword
	return step
}

// AsCheck marks a step as a non-fatal check.
func AsCheck(step Step) Step {
	step.Kind = Check
	return step
}

// Journey is one named scenario, composed of steps drawn from the shared
// vocabulary in steps.go and run once per harness in Harnesses. No journey
// inlines a step.
//
// Network overrides the harness's spec network profile for this journey when set
// (driver.Spec.NetworkOrDefault otherwise). The attribution journey sets it to a
// profile that admits the stub, so the run's baseline is broad enough for the
// analyser to actually narrow it and the network-not-widened assertion to have a
// posture to check.
//
//exhaustruct:ignore
type Journey struct {
	Name      string
	Harnesses []string
	Network   string
	Steps     []Step
}

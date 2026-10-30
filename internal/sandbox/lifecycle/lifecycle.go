// Package lifecycle holds the run lifecycle's shared vocabulary: the stage ids
// of specs/lifecycle.md, a trace that records the stages an invocation reaches,
// and the loop that runs an ordered stage list. It holds no stage logic: cmd,
// vm and session own their stages and record them here, so the three packages
// contribute to one ordered trace.
package lifecycle

import "slices"

// StageID names one run lifecycle stage. The values are the ids
// specs/lifecycle.md uses.
type StageID string

// The stage ids, in lifecycle order.
const (
	Preflight       StageID = "preflight"
	UpgradeCheck    StageID = "upgrade-check"
	Prune           StageID = "prune"
	ResolveIdentity StageID = "resolve-identity"
	ResolveVersion  StageID = "resolve-version"
	EnsureImage     StageID = "ensure-image"
	EnsureHome      StageID = "ensure-home"
	PlanReconfig    StageID = "plan-reconfig"
	EnsureVM        StageID = "ensure-vm"
	SetupSandbox    StageID = "setup-sandbox"
	Attach          StageID = "attach"
)

// Order returns every stage id in lifecycle order.
//
//deadcode:ignore reason="the canonical stage order the spec and trace tests assert against; nothing orders stages at run time"
func Order() []StageID {
	return []StageID{
		Preflight,
		UpgradeCheck,
		Prune,
		ResolveIdentity,
		ResolveVersion,
		EnsureImage,
		EnsureHome,
		PlanReconfig,
		EnsureVM,
		SetupSandbox,
		Attach,
	}
}

// Trace records the stages an invocation reaches, in order. A stage is recorded
// when it is reached, before its work runs, so a failing stage is the last one
// recorded; a mode that makes a stage a no-op still records it. A nil *Trace
// records nothing, so callers that do not observe the lifecycle pass nil.
type Trace struct {
	stages []StageID
}

// Record appends id to the trace.
func (trace *Trace) Record(id StageID) {
	if trace == nil {
		return
	}
	trace.stages = append(trace.stages, id)
}

// Stages returns the recorded stage ids in the order they were reached.
//
//deadcode:ignore reason="the trace is test instrumentation, read back only by tests"
func (trace *Trace) Stages() []StageID {
	if trace == nil {
		return nil
	}
	return slices.Clone(trace.stages)
}

// Stage is one element of an ordered stage list: its id and its work.
type Stage struct {
	ID  StageID
	Run func() error
}

// Run runs stages in order, recording each id into trace as it is reached, and
// stops at the first error. A stage list run here cannot skip the trace.
func Run(trace *Trace, stages []Stage) error {
	for _, stage := range stages {
		trace.Record(stage.ID)
		if err := stage.Run(); err != nil {
			return err
		}
	}
	return nil
}

package agent

// Resolved is a selected agent profile together with the optional capabilities
// the run currently consumes, resolved once at a run boundary by Resolve. It is
// deliberately narrow: a capability is a field here only while production code
// reads it, so this type does not recreate the shallow "assert whatever you
// might need" surface it replaces. A nil field means the profile does not
// provide that capability. Profile is the selected profile itself; the
// capability fields are the same values the AsX helpers would return.
type Resolved struct {
	Profile Agent

	Daemon           DaemonProvider
	Worktree         WorktreeProvider
	Version          VersionProvider
	Upgrade          UpgradeChecker
	GuestTools       GuestToolRequirer
	DetachedWorktree DetachedWorktreeReporter
}

// Resolve resolves the capabilities the run consumes from a once. A caller at
// the run boundary resolves the selected profile here and passes the value on,
// so each capability is discovered by type assertion in one place rather than
// re-asserted by every consumer of the same run.
func Resolve(a Agent) Resolved {
	var r Resolved
	r.Profile = a
	r.Daemon, _ = AsDaemonProvider(a)
	r.Worktree, _ = AsWorktreeProvider(a)
	r.Version, _ = AsVersionProvider(a)
	r.Upgrade, _ = AsUpgradeChecker(a)
	r.GuestTools, _ = AsGuestToolRequirer(a)
	r.DetachedWorktree, _ = AsDetachedWorktreeReporter(a)
	return r
}

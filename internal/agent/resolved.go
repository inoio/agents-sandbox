package agent

// Resolved is a selected agent profile together with its optional
// capabilities, resolved once at a run boundary by Resolve. A nil capability
// field means the profile does not provide it, so a consumer reads the field
// instead of re-asserting the interface on the profile. Agent is the selected
// profile itself; the capability fields are the same values the AsX helpers
// would return.
type Resolved struct {
	Agent Agent

	Daemon           DaemonProvider
	SessionStatus    SessionStatusProvider
	Worktree         WorktreeProvider
	Attach           AttachRunner
	Version          VersionProvider
	Upgrade          UpgradeChecker
	ConfigMerger     ConfigMerger
	Provisioner      Provisioner
	GuestTools       GuestToolRequirer
	EventStream      EventStreamProvider
	ConfigDirAliaser ConfigDirAliaser
	DetachedWorktree DetachedWorktreeReporter
}

// Resolve resolves a's optional capabilities once. A caller at the run boundary
// resolves the selected profile here and passes the value on, so each
// capability is discovered by type assertion in one place rather than
// re-asserted by every consumer of the same run.
func Resolve(a Agent) Resolved {
	var r Resolved
	r.Agent = a
	r.Daemon, _ = AsDaemonProvider(a)
	r.SessionStatus, _ = AsSessionStatusProvider(a)
	r.Worktree, _ = AsWorktreeProvider(a)
	r.Attach, _ = AsAttachRunner(a)
	r.Version, _ = AsVersionProvider(a)
	r.Upgrade, _ = AsUpgradeChecker(a)
	r.ConfigMerger, _ = AsConfigMerger(a)
	r.Provisioner, _ = AsProvisioner(a)
	r.GuestTools, _ = AsGuestToolRequirer(a)
	r.EventStream, _ = AsEventStreamProvider(a)
	r.ConfigDirAliaser, _ = AsConfigDirAliaser(a)
	r.DetachedWorktree, _ = AsDetachedWorktreeReporter(a)
	return r
}

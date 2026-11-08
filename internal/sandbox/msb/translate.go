package msb

import msbSdk "github.com/superradcompany/microsandbox/sdk/go"

// The projection registers below name every exported field of the SDK config
// struct a translator projects. The drift test reflects over each struct and
// fails when a field is neither projected nor listed in the matching
// notModeled register, so an SDK release adding a field is caught here rather
// than silently ignored.
var (
	execProjected = map[string]struct{}{
		"Cwd":       {},
		"Timeout":   {},
		"StdinPipe": {},
		"TTY":       {},
		"User":      {},
		"Env":       {},
	}

	attachProjected = map[string]struct{}{
		"Cwd":        {},
		"User":       {},
		"Env":        {},
		"DetachKeys": {},
	}

	logProjected = map[string]struct{}{
		"Tail":    {},
		"Since":   {},
		"Until":   {},
		"Sources": {},
	}

	compactionProjected = map[string]struct{}{
		"Layers":       {},
		"DryRun":       {},
		"Disk":         {},
		"RootDiskOnly": {},
	}

	// execNotModeled names msbSdk.ExecConfig fields the launcher deliberately
	// drops. Empty today: every exported field is projected.
	execNotModeled = map[string]string{}

	// attachNotModeled names msbSdk.AttachConfig fields the launcher
	// deliberately drops. Empty today.
	attachNotModeled = map[string]string{}

	// logNotModeled names msbSdk.LogOptions fields the launcher deliberately
	// drops. Empty today.
	logNotModeled = map[string]string{}

	// compactionNotModeled names msbSdk.DiskCompactionOptions fields the
	// launcher deliberately drops. Empty today.
	compactionNotModeled = map[string]string{}
)

// execOptions translates an ExecRequest into the SDK exec options. Options
// equal to the SDK defaults are omitted so a zero request yields no options.
func execOptions(r ExecRequest) []msbSdk.ExecOption {
	var opts []msbSdk.ExecOption
	if r.User != "" {
		opts = append(opts, msbSdk.WithExecUser(r.User))
	}
	if r.Cwd != "" {
		opts = append(opts, msbSdk.WithExecCwd(r.Cwd))
	}
	if len(r.Env) > 0 {
		opts = append(opts, msbSdk.WithExecEnv(r.Env))
	}
	if r.TTY {
		opts = append(opts, msbSdk.WithExecTTY(r.TTY))
	}
	if r.Timeout != 0 {
		opts = append(opts, msbSdk.WithExecTimeout(r.Timeout))
	}
	if r.StdinPipe {
		opts = append(opts, msbSdk.WithExecStdinPipe())
	}
	return opts
}

// attachOptions translates an AttachRequest into the SDK attach options,
// omitting options equal to the SDK defaults.
func attachOptions(r AttachRequest) []msbSdk.AttachOption {
	var opts []msbSdk.AttachOption
	if r.Cwd != "" {
		opts = append(opts, msbSdk.WithAttachCwd(r.Cwd))
	}
	if r.User != "" {
		opts = append(opts, msbSdk.WithAttachUser(r.User))
	}
	if len(r.Env) > 0 {
		opts = append(opts, msbSdk.WithAttachEnv(r.Env))
	}
	if r.DetachKeys != "" {
		opts = append(opts, msbSdk.WithAttachDetachKeys(r.DetachKeys))
	}
	return opts
}

// jobExecOptions translates a JobSpec into the SDK detached-job exec options.
// A job spec is an exec request, so the exec options are reused; the SDK's
// ExecOption implements JobExecOption.
//
// StdinPipe is asymmetric with a plain exec: ExecDetached defaults it to true
// (retained stdin pipes) while Exec defaults it to false. A zero JobSpec
// therefore keeps stdin piped, StdinPipe: true is redundant, and
// StdinPipe: false is not expressible (it would need WithExecStdinNull, which
// would overwrite the retained-pipe default no caller wants changed). See the
// JobSpec doc comment.
func jobExecOptions(s JobSpec) []msbSdk.JobExecOption {
	execOpts := execOptions(s.ExecRequest)
	if len(execOpts) == 0 {
		return nil
	}
	jobOpts := make([]msbSdk.JobExecOption, 0, len(execOpts))
	for _, opt := range execOpts {
		jobOpts = append(jobOpts, opt)
	}
	return jobOpts
}

// jobListOptions translates a JobQuery into the SDK detached-job listing
// options, omitting options equal to the SDK defaults.
func jobListOptions(q JobQuery) []msbSdk.JobListOption {
	var opts []msbSdk.JobListOption
	if q.All {
		opts = append(opts, msbSdk.WithAllJobs())
	}
	if q.Limit != 0 {
		opts = append(opts, msbSdk.WithJobLimit(q.Limit))
	}
	if q.Cursor != "" {
		opts = append(opts, msbSdk.WithJobCursor(q.Cursor))
	}
	return opts
}

// lifecycleOptions translates a LifecycleOptions into the SDK stop, kill and
// destroy options. A zero Timeout means unset, so no timeout option is emitted
// for any of the three; Force is meaningful only for destroy.
func lifecycleOptions(o LifecycleOptions) ([]msbSdk.StopOption, []msbSdk.KillOption, []msbSdk.DestroyOption) {
	var stop []msbSdk.StopOption
	var kill []msbSdk.KillOption
	var destroy []msbSdk.DestroyOption
	if o.Timeout != 0 {
		stop = append(stop, msbSdk.WithStopTimeout(o.Timeout))
		kill = append(kill, msbSdk.WithKillTimeout(o.Timeout))
		destroy = append(destroy, msbSdk.WithDestroyTimeout(o.Timeout))
	}
	if o.Force {
		destroy = append(destroy, msbSdk.WithDestroyForce())
	}
	return stop, kill, destroy
}

// logOptions translates a LogQuery into the SDK log filter.
func logOptions(q LogQuery) msbSdk.LogOptions {
	return msbSdk.LogOptions{
		Tail:    q.Tail,
		Since:   q.Since,
		Until:   q.Until,
		Sources: logSources(q.Sources),
	}
}

// logSources maps launcher log sources to the SDK's. A nil input stays nil so
// the SDK applies its default source set.
func logSources(sources []LogSource) []msbSdk.LogSource {
	if sources == nil {
		return nil
	}
	out := make([]msbSdk.LogSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, msbSdk.LogSource(source))
	}
	return out
}

// compactionOptions translates a CompactionRequest into the SDK disk
// compaction selection.
func compactionOptions(r CompactionRequest) msbSdk.DiskCompactionOptions {
	return msbSdk.DiskCompactionOptions{
		Layers:       r.Layers,
		DryRun:       r.DryRun,
		Disk:         r.Disk,
		RootDiskOnly: r.RootDiskOnly,
	}
}

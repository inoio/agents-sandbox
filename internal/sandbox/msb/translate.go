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

	// storagePruneProjected names every field of msbSdk.StoragePruneOptions
	// the launcher projects onto PruneRequest.
	storagePruneProjected = map[string]struct{}{
		"DryRun":           {},
		"OlderThanSeconds": {},
	}

	// storagePruneNotModeled names msbSdk.StoragePruneOptions fields the
	// launcher deliberately drops. Empty today: every field is projected.
	storagePruneNotModeled = map[string]string{}

	// jobLogProjected names every field of msbSdk.JobLogOptions the launcher
	// projects onto JobLogQuery.
	jobLogProjected = map[string]struct{}{
		"Tail":       {},
		"Since":      {},
		"Until":      {},
		"Sources":    {},
		"FromCursor": {},
		"Follow":     {},
	}

	// jobLogNotModeled names msbSdk.JobLogOptions fields the launcher
	// deliberately drops. Empty today: every field is projected.
	jobLogNotModeled = map[string]string{}
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

// storagePruneOptions translates a PruneRequest into the SDK prune selection.
func storagePruneOptions(req PruneRequest) msbSdk.StoragePruneOptions {
	return msbSdk.StoragePruneOptions{
		DryRun:           req.DryRun,
		OlderThanSeconds: req.OlderThanSeconds,
	}
}

// storageReportFrom translates the SDK storage observation, keeping optional
// counters nil so unknown stays unknown. A nil report stays nil.
func storageReportFrom(report *msbSdk.StorageUsageReport) *StorageReport {
	if report == nil {
		return nil
	}
	return &StorageReport{
		Images:         storageCategoryFrom(report.Images),
		Snapshots:      storageCategoryFrom(report.Snapshots),
		Sandboxes:      storageCategoryFrom(report.Sandboxes),
		Volumes:        storageCategoryFrom(report.Volumes),
		BranchMemory:   storageCategoryFrom(report.BranchMemory),
		SnapshotMemory: storageCategoryFrom(report.SnapshotMemory),
		Notes:          report.Notes,
	}
}

// storageCategoryFrom translates one category's totals and items, preserving
// nil collections as nil.
func storageCategoryFrom(category msbSdk.StorageCategoryUsage) StorageCategory {
	return StorageCategory{
		Count:                   category.Count,
		InUse:                   category.InUse,
		LogicalBytes:            category.LogicalBytes,
		AllocatedBytes:          category.AllocatedBytes,
		ReclaimableLogicalBytes: category.ReclaimableLogicalBytes,
		Items:                   storageItemsFrom(category.Items),
		Notes:                   category.Notes,
	}
}

// storageItemsFrom translates items, preserving nil as nil.
func storageItemsFrom(items []msbSdk.StorageItemUsage) []StorageItem {
	if items == nil {
		return nil
	}
	out := make([]StorageItem, len(items))
	for i, item := range items {
		out[i] = storageItemFrom(item)
	}
	return out
}

// storageItemFrom translates one storage item field for field.
func storageItemFrom(item msbSdk.StorageItemUsage) StorageItem {
	return StorageItem{
		Name:           item.Name,
		Path:           item.Path,
		LogicalBytes:   item.LogicalBytes,
		AllocatedBytes: item.AllocatedBytes,
		InUse:          item.InUse,
		Reclaimable:    item.Reclaimable,
		Reasons:        item.Reasons,
	}
}

// pruneReportFrom translates the SDK prune report, keeping only the reclaimed
// totals the launcher surfaces. A nil report stays nil.
func pruneReportFrom(report *msbSdk.StoragePruneReport) *PruneReport {
	if report == nil {
		return nil
	}
	return &PruneReport{
		FilesRemoved:           report.FilesRemoved,
		LogicalBytesRemoved:    report.LogicalBytesRemoved,
		PhysicalBytesReclaimed: report.PhysicalBytesReclaimed,
	}
}

// jobLogOptions translates a JobLogQuery into the SDK detached-job log filter.
func jobLogOptions(q JobLogQuery) msbSdk.JobLogOptions {
	return msbSdk.JobLogOptions{
		Tail:       q.Tail,
		Since:      q.Since,
		Until:      q.Until,
		Sources:    q.Sources,
		FromCursor: q.FromCursor,
		Follow:     q.Follow,
	}
}

// jobInfoFrom translates the SDK job metadata field for field. A nil info
// stays nil.
func jobInfoFrom(info *msbSdk.JobInfo) *JobInfo {
	if info == nil {
		return nil
	}
	return &JobInfo{
		ID:            info.ID,
		RuntimeBootID: info.RuntimeBootID,
		Command:       info.Command,
		State:         info.State,
		TTY:           info.TTY,
		StdinClosed:   info.StdinClosed,
		PID:           info.PID,
		CreatedAt:     info.CreatedAt,
		StartedAt:     info.StartedAt,
		FinishedAt:    info.FinishedAt,
		ExitCode:      info.ExitCode,
		TimedOut:      info.TimedOut,
		Failure:       execFailureFrom(info.Failure),
		Error:         info.Error,
	}
}

// jobInfosFrom translates a slice of SDK job metadata, preserving nil as nil.
func jobInfosFrom(infos []msbSdk.JobInfo) []JobInfo {
	if infos == nil {
		return nil
	}
	out := make([]JobInfo, len(infos))
	for i, info := range infos {
		out[i] = *jobInfoFrom(&info)
	}
	return out
}

// execFailureFrom translates the SDK's structured launch failure field for
// field. A nil failure stays nil.
func execFailureFrom(failure *msbSdk.ExecFailure) *ExecFailure {
	if failure == nil {
		return nil
	}
	return &ExecFailure{
		Kind:      failure.Kind,
		Errno:     failure.Errno,
		ErrnoName: failure.ErrnoName,
		Message:   failure.Message,
		Path:      failure.Path,
	}
}

// jobLogEntriesFrom translates a slice of SDK job log entries, preserving nil
// as nil.
func jobLogEntriesFrom(entries []msbSdk.JobLogEntry) []JobLogEntry {
	if entries == nil {
		return nil
	}
	out := make([]JobLogEntry, len(entries))
	for i, entry := range entries {
		out[i] = JobLogEntry{
			Timestamp: entry.Timestamp,
			Source:    entry.Source,
			Data:      entry.Data,
			Cursor:    entry.Cursor,
		}
	}
	return out
}

// logEntriesFrom translates a slice of SDK sandbox log entries, preserving nil
// as nil.
func logEntriesFrom(entries []msbSdk.LogEntry) []LogEntry {
	if entries == nil {
		return nil
	}
	out := make([]LogEntry, len(entries))
	for i, entry := range entries {
		out[i] = LogEntry{
			Source:    LogSource(entry.Source),
			SessionID: entry.SessionID,
			Timestamp: entry.Timestamp,
			Data:      entry.Data,
			Cursor:    entry.Cursor,
		}
	}
	return out
}

// metricsFrom translates the SDK metrics snapshot field for field. A nil
// snapshot stays nil.
func metricsFrom(metrics *msbSdk.Metrics) *Metrics {
	if metrics == nil {
		return nil
	}
	return &Metrics{
		CPUPercent:              metrics.CPUPercent,
		VCPUTimeNs:              metrics.VCPUTimeNs,
		MemoryBytes:             metrics.MemoryBytes,
		MemoryAvailableBytes:    metrics.MemoryAvailableBytes,
		MemoryHostResidentBytes: metrics.MemoryHostResidentBytes,
		MemoryLimitBytes:        metrics.MemoryLimitBytes,
		DiskReadBytes:           metrics.DiskReadBytes,
		DiskWriteBytes:          metrics.DiskWriteBytes,
		NetRxBytes:              metrics.NetRxBytes,
		NetTxBytes:              metrics.NetTxBytes,
		UpperUsedBytes:          metrics.UpperUsedBytes,
		UpperFreeBytes:          metrics.UpperFreeBytes,
		UpperHostAllocatedBytes: metrics.UpperHostAllocatedBytes,
		Uptime:                  metrics.Uptime,
	}
}

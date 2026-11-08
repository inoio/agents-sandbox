package msb

import (
	"slices"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// The projection registers below name every exported field of an SDK struct a
// translator projects — both the option (input) structs and the return structs
// the phase-3 translators copy field for field. The drift test reflects over
// each struct and fails when a field is neither projected nor listed in the
// matching notModeled register, so an SDK release adding a field is caught here
// rather than silently ignored.
//
//nolint:goconst // The repeated strings are SDK field names the registers pin verbatim, not extractable constants.
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

	// storageUsageReportProjected names every field of
	// msbSdk.StorageUsageReport the launcher projects onto StorageReport;
	// storageUsageReportNotModeled is empty because every field is projected.
	storageUsageReportProjected = map[string]struct{}{
		"Images":         {},
		"Snapshots":      {},
		"Sandboxes":      {},
		"Volumes":        {},
		"BranchMemory":   {},
		"SnapshotMemory": {},
		"Notes":          {},
	}
	storageUsageReportNotModeled = map[string]string{}

	// storageCategoryProjected names every field of
	// msbSdk.StorageCategoryUsage the launcher projects onto StorageCategory;
	// storageCategoryNotModeled is empty because every field is projected.
	storageCategoryProjected = map[string]struct{}{
		"Count":                   {},
		"InUse":                   {},
		"LogicalBytes":            {},
		"AllocatedBytes":          {},
		"ReclaimableLogicalBytes": {},
		"Items":                   {},
		"Notes":                   {},
	}
	storageCategoryNotModeled = map[string]string{}

	// storageItemProjected names every field of msbSdk.StorageItemUsage the
	// launcher projects onto StorageItem; storageItemNotModeled is empty
	// because every field is projected.
	storageItemProjected = map[string]struct{}{
		"Name":           {},
		"Path":           {},
		"LogicalBytes":   {},
		"AllocatedBytes": {},
		"InUse":          {},
		"Reclaimable":    {},
		"Reasons":        {},
	}
	storageItemNotModeled = map[string]string{}

	// storagePruneReportProjected names the msbSdk.StoragePruneReport fields
	// the launcher projects onto PruneReport. The notModeled register names
	// the rest, which PruneReport deliberately drops: it keeps only the
	// reclaimed totals.
	storagePruneReportProjected = map[string]struct{}{
		"FilesRemoved":           {},
		"LogicalBytesRemoved":    {},
		"PhysicalBytesReclaimed": {},
	}
	storagePruneReportNotModeled = map[string]string{
		"DryRun":    "the caller issued the prune, so the dry-run echo adds nothing",
		"Entries":   "per-file outcomes are not surfaced",
		"Truncated": "the entries-listing cap is not surfaced",
	}

	// jobInfoProjected names every field of msbSdk.JobInfo the launcher
	// projects onto JobInfo; jobInfoNotModeled is empty because every field
	// is projected.
	jobInfoProjected = map[string]struct{}{
		"ID":            {},
		"RuntimeBootID": {},
		"Command":       {},
		"State":         {},
		"TTY":           {},
		"StdinClosed":   {},
		"PID":           {},
		"CreatedAt":     {},
		"StartedAt":     {},
		"FinishedAt":    {},
		"ExitCode":      {},
		"TimedOut":      {},
		"Failure":       {},
		"Error":         {},
	}
	jobInfoNotModeled = map[string]string{}

	// jobLogEntryProjected names every field of msbSdk.JobLogEntry the
	// launcher projects onto JobLogEntry; jobLogEntryNotModeled is empty
	// because every field is projected.
	jobLogEntryProjected = map[string]struct{}{
		"Timestamp": {},
		"Source":    {},
		"Data":      {},
		"Cursor":    {},
	}
	jobLogEntryNotModeled = map[string]string{}

	// execFailureProjected names every field of msbSdk.ExecFailure the
	// launcher projects onto ExecFailure; execFailureNotModeled is empty
	// because every field is projected.
	execFailureProjected = map[string]struct{}{
		"Kind":      {},
		"Errno":     {},
		"ErrnoName": {},
		"Message":   {},
		"Path":      {},
	}
	execFailureNotModeled = map[string]string{}

	// logEntryProjected names every field of msbSdk.LogEntry the launcher
	// projects onto LogEntry; logEntryNotModeled is empty because every field
	// is projected.
	logEntryProjected = map[string]struct{}{
		"Source":    {},
		"SessionID": {},
		"Timestamp": {},
		"Data":      {},
		"Cursor":    {},
	}
	logEntryNotModeled = map[string]string{}

	// metricsProjected names every field of msbSdk.Metrics the launcher
	// projects onto Metrics; metricsNotModeled is empty because every field
	// is projected.
	metricsProjected = map[string]struct{}{
		"CPUPercent":              {},
		"VCPUTimeNs":              {},
		"MemoryBytes":             {},
		"MemoryAvailableBytes":    {},
		"MemoryHostResidentBytes": {},
		"MemoryLimitBytes":        {},
		"DiskReadBytes":           {},
		"DiskWriteBytes":          {},
		"NetRxBytes":              {},
		"NetTxBytes":              {},
		"UpperUsedBytes":          {},
		"UpperFreeBytes":          {},
		"UpperHostAllocatedBytes": {},
		"Uptime":                  {},
	}
	metricsNotModeled = map[string]string{}

	// imageConfigProjected names every field of msbSdk.ImageConfig the
	// launcher projects onto ImageConfig; imageConfigNotModeled is empty
	// because every field is projected.
	imageConfigProjected = map[string]struct{}{
		"Digest":     {},
		"Env":        {},
		"Cmd":        {},
		"Entrypoint": {},
		"WorkingDir": {},
		"User":       {},
		"Labels":     {},
		"StopSignal": {},
	}
	imageConfigNotModeled = map[string]string{}

	// imagePruneReportProjected names every field of
	// msbSdk.ImagePruneReport the launcher projects onto ImagePruneReport;
	// imagePruneReportNotModeled is empty because every field is projected.
	imagePruneReportProjected = map[string]struct{}{
		"SkippedInUse":     {},
		"ImageRefsRemoved": {},
		"ManifestsRemoved": {},
		"LayersRemoved":    {},
		"FsmetaRemoved":    {},
		"VMDKRemoved":      {},
		"BytesReclaimed":   {},
	}
	imagePruneReportNotModeled = map[string]string{}

	// imageLayerProjected names every field of msbSdk.ImageLayer the
	// launcher projects onto ImageLayer; imageLayerNotModeled is empty
	// because every field is projected.
	imageLayerProjected = map[string]struct{}{
		"DiffID":              {},
		"BlobDigest":          {},
		"MediaType":           {},
		"CompressedSizeBytes": {},
		"ErofsSizeBytes":      {},
		"Position":            {},
	}
	imageLayerNotModeled = map[string]string{}

	// sandboxConfigProjected names every field of msbSdk.SandboxConfig the
	// adapter sets through a creation option.
	sandboxConfigProjected = map[string]struct{}{
		"Image":        {},
		"Labels":       {},
		"Volumes":      {},
		"Secrets":      {},
		"Env":          {},
		"Workdir":      {},
		"CPUs":         {},
		"MaxCPUs":      {},
		"MemoryMiB":    {},
		"MaxMemoryMiB": {},
		"Detached":     {},
		"IdleTimeout":  {},
		"PortBindings": {},
		"Network":      {},
		"RootDisk":     {},
	}

	// sandboxConfigNotModeled names msbSdk.SandboxConfig fields the launcher
	// deliberately drops at creation. The reason is the contract: a field that
	// later becomes a launcher input is removed from here and added above.
	sandboxConfigNotModeled = map[string]string{
		"Name":               "the launcher names the sandbox itself",
		"ImageFstype":        "the launcher uses OCI images, not raw disk images",
		"ImageBind":          "the launcher uses OCI images, not bind rootfs",
		"OCIUpperSizeMiB":    "deprecated SDK field; the launcher sets RootDisk",
		"CPUPlacement":       "host placement policy is left to the runtime default",
		"PlacementProfile":   "host placement profile is left to the runtime default",
		"NestedVirt":         "nested virtualization is not used by the launcher",
		"THP":                "transparent huge-page policy is left to the runtime default",
		"Shell":              "the guest shell comes from the image",
		"SecurityProfile":    "the in-guest security profile is left to the runtime default",
		"DeploymentProfile":  "the deployment profile is left to the runtime default",
		"Hostname":           "hostname is left to the runtime",
		"User":               "the guest user is set by the image and attach/exec requests",
		"Replace":            "a taken name fails closed; the launcher never replaces on create",
		"ReplaceWithTimeout": "only meaningful with Replace, which the launcher does not set",
		"Ephemeral":          "the launcher manages the sandbox lifecycle explicitly",
		"Entrypoint":         "the image entrypoint is used",
		"Cmd":                "the image cmd is used",
		"Init":               "PID 1 init is left to the runtime default",
		"LogLevel":           "log level is left to the runtime default",
		"QuietLogs":          "quiet-logs is left to the runtime default",
		"Scripts":            "startup hooks come from the image",
		"PullPolicy":         "image pulling is driven by the launcher's image stage",
		"MaxDuration":        "no maximum sandbox duration is set",
		"RegistryAuth":       "registry credentials are not passed by the launcher",
		"RegistryInsecure":   "plain-HTTP pulls are not used by the launcher",
		"RegistryCACerts":    "registry CA certificates are not passed by the launcher",
		"RegistryCACertPaths": "registry CA certificate paths are not passed by the " +
			"launcher",
		"Ports":             "the launcher publishes ports through explicit PortBindings",
		"PortsUDP":          "the launcher publishes ports through explicit PortBindings",
		"Vsock":             "vsock routes are not used by the launcher",
		"Proxy":             "an outbound proxy is not configured by the launcher",
		"Patches":           "rootfs patches are not used by the launcher",
		"ociUpperSizeSet":   "unexported: set internally when a root disk size is provided",
		"registryAnonymous": "unexported: set internally by registry-auth options",
	}

	// volumeConfigProjected names every field of msbSdk.VolumeConfig the
	// launcher projects onto VolumeSpec.
	volumeConfigProjected = map[string]struct{}{
		"Kind": {},
	}

	// volumeConfigNotModeled names msbSdk.VolumeConfig fields the launcher
	// deliberately drops at creation.
	volumeConfigNotModeled = map[string]string{
		"QuotaMiB": "the launcher creates unquoted directory volumes",
		"SizeMiB":  "the launcher creates directory volumes, not disk volumes",
		"Labels":   "the launcher does not label named volumes",
	}
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

// imageConfigFrom translates the SDK image config block field for field. A nil
// config stays nil.
func imageConfigFrom(config *msbSdk.ImageConfig) *ImageConfig {
	if config == nil {
		return nil
	}
	return &ImageConfig{
		Digest:     config.Digest,
		Env:        config.Env,
		Cmd:        config.Cmd,
		Entrypoint: config.Entrypoint,
		WorkingDir: config.WorkingDir,
		User:       config.User,
		Labels:     config.Labels,
		StopSignal: config.StopSignal,
	}
}

// imageLayersFrom translates a slice of SDK image layers, preserving nil as
// nil.
func imageLayersFrom(layers []msbSdk.ImageLayer) []ImageLayer {
	if layers == nil {
		return nil
	}
	out := make([]ImageLayer, len(layers))
	for i, layer := range layers {
		out[i] = ImageLayer{
			DiffID:              layer.DiffID,
			BlobDigest:          layer.BlobDigest,
			MediaType:           layer.MediaType,
			CompressedSizeBytes: layer.CompressedSizeBytes,
			ErofsSizeBytes:      layer.ErofsSizeBytes,
			Position:            layer.Position,
		}
	}
	return out
}

// imagePruneReportFrom translates the SDK image prune report field for field.
// A nil report stays nil.
func imagePruneReportFrom(report *msbSdk.ImagePruneReport) *ImagePruneReport {
	if report == nil {
		return nil
	}
	return &ImagePruneReport{
		SkippedInUse:     report.SkippedInUse,
		ImageRefsRemoved: report.ImageRefsRemoved,
		ManifestsRemoved: report.ManifestsRemoved,
		LayersRemoved:    report.LayersRemoved,
		FsmetaRemoved:    report.FsmetaRemoved,
		VMDKRemoved:      report.VMDKRemoved,
		BytesReclaimed:   report.BytesReclaimed,
	}
}

// sandboxStatusFrom translates the SDK sandbox status onto the launcher enum.
// The launcher mirrors every SDK value, so a raw conversion preserves an
// unknown status rather than collapsing it to a known one.
func sandboxStatusFrom(status msbSdk.SandboxStatus) SandboxStatus {
	return SandboxStatus(status)
}

// backendKindFrom translates the SDK backend kind onto the launcher enum.
func backendKindFrom(kind msbSdk.BackendKind) BackendKind {
	return BackendKind(kind)
}

// volumeKindFrom translates the SDK volume kind onto the launcher enum.
func volumeKindFrom(kind msbSdk.VolumeKind) VolumeKind {
	return VolumeKind(kind)
}

// volumeKindTo translates the launcher volume kind onto the SDK enum.
func volumeKindTo(kind VolumeKind) msbSdk.VolumeKind {
	return msbSdk.VolumeKind(kind)
}

// sandboxOptions translates a SandboxSpec into the SDK option list in the order
// the launcher has always emitted it: image, labels, mounts, secrets, env,
// workdir, CPUs and memory bounds, detached, idle timeout, then an optional
// managed root disk, port bindings, and the network policy. Options equal to
// the SDK defaults are omitted, so a spec that sets nothing but a network
// policy (the throwaway task sandboxes) does not accidentally detach, bound, or
// size the sandbox. A network profile the runtime rejects fails the whole
// translation.
func sandboxOptions(spec SandboxSpec) ([]msbSdk.SandboxOption, error) {
	network, err := networkConfig(spec.Network)
	if err != nil {
		return nil, err
	}
	opts := []msbSdk.SandboxOption{msbSdk.WithImage(spec.Image)}
	if len(spec.Labels) > 0 {
		opts = append(opts, msbSdk.WithLabels(spec.Labels))
	}
	if len(spec.Mounts) > 0 {
		opts = append(opts, msbSdk.WithMounts(mountConfigs(spec.Mounts)))
	}
	if len(spec.Secrets) > 0 {
		opts = append(opts, msbSdk.WithSecrets(secretEntries(spec.Secrets)...))
	}
	if len(spec.Env) > 0 {
		opts = append(opts, msbSdk.WithEnv(spec.Env))
	}
	if spec.Workdir != "" {
		opts = append(opts, msbSdk.WithWorkdir(spec.Workdir))
	}
	if spec.CPUs != 0 {
		opts = append(opts, msbSdk.WithCPUs(spec.CPUs))
	}
	if spec.MaxCPUs != 0 {
		opts = append(opts, msbSdk.WithMaxCPUs(spec.MaxCPUs))
	}
	if spec.MemoryMiB != 0 {
		opts = append(opts, msbSdk.WithMemory(spec.MemoryMiB))
	}
	if spec.MaxMemoryMiB != 0 {
		opts = append(opts, msbSdk.WithMaxMemory(spec.MaxMemoryMiB))
	}
	if spec.Detached {
		opts = append(opts, msbSdk.WithDetached())
	}
	if spec.IdleTimeout != 0 {
		opts = append(opts, msbSdk.WithIdleTimeout(spec.IdleTimeout))
	}
	if spec.RootDiskMiB > 0 {
		opts = append(opts, msbSdk.WithRootDisk(msbSdk.RootDisk.Managed(spec.RootDiskMiB)))
	}
	if len(spec.Ports) > 0 {
		opts = append(opts, msbSdk.WithPortBindings(portBindings(spec.Ports)...))
	}
	// The launcher always configures a network policy for a project VM (its
	// default is deny-by-default egress), so the network option is emitted even
	// for an empty spec.
	opts = append(opts, msbSdk.WithNetwork(network))
	return opts, nil
}

// mountConfigs translates a guest-path-keyed map of mount descriptors,
// preserving nil as nil.
func mountConfigs(mounts map[string]MountConfig) map[string]msbSdk.MountConfig {
	if mounts == nil {
		return nil
	}
	out := make(map[string]msbSdk.MountConfig, len(mounts))
	for target, mount := range mounts {
		out[target] = mountConfig(mount)
	}
	return out
}

// mountConfig rebuilds the SDK mount descriptor for a launcher descriptor via
// the SDK factory matching its kind, copying exactly the fields the descriptor
// carries.
func mountConfig(m MountConfig) msbSdk.MountConfig {
	options := msbSdk.MountOptions{
		Readonly:           m.Readonly,
		Noexec:             m.Noexec,
		Nosuid:             m.Nosuid,
		Nodev:              m.Nodev,
		StatVirtualization: msbSdk.StatVirtualization(m.StatVirtualization),
		HostPermissions:    msbSdk.HostPermissions(m.HostPermissions),
		Owner:              sdkMountOwner(m.Owner),
	}
	switch m.Kind {
	case MountKindBind:
		options.QuotaMiB = m.QuotaMiB
		return msbSdk.Mount.Bind(m.Bind, options)
	case MountKindNamed:
		return msbSdk.Mount.Named(m.Named, options)
	case MountKindTmpfs:
		return msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{
			SizeMiB:  m.SizeMiB,
			Readonly: m.Readonly,
			Noexec:   m.Noexec,
			Nosuid:   m.Nosuid,
			Nodev:    m.Nodev,
		})
	case MountKindDisk:
		return msbSdk.Mount.Disk(m.Disk, msbSdk.DiskOptions{
			Format:   m.Format,
			Fstype:   m.Fstype,
			Readonly: m.Readonly,
			Noexec:   m.Noexec,
			Nosuid:   m.Nosuid,
			Nodev:    m.Nodev,
		})
	case MountKindOwned:
		return msbSdk.Mount.Owned(msbSdk.OwnedVolumeOptions{
			Kind:               volumeKindTo(VolumeKind(m.Owned)),
			SizeMiB:            m.SizeMiB,
			QuotaMiB:           m.QuotaMiB,
			Readonly:           m.Readonly,
			Noexec:             m.Noexec,
			Nosuid:             m.Nosuid,
			Nodev:              m.Nodev,
			StatVirtualization: msbSdk.StatVirtualization(m.StatVirtualization),
			HostPermissions:    msbSdk.HostPermissions(m.HostPermissions),
			Owner:              sdkMountOwner(m.Owner),
		})
	default:
		return msbSdk.MountConfig{}
	}
}

// sdkMountOwner translates a launcher mount owner, preserving nil.
func sdkMountOwner(owner *MountOwner) *msbSdk.MountOwner {
	if owner == nil {
		return nil
	}
	return &msbSdk.MountOwner{UID: owner.UID, GID: owner.GID}
}

// portBindings translates launcher port bindings, preserving nil as nil.
func portBindings(bindings []PortBinding) []msbSdk.PortBinding {
	if bindings == nil {
		return nil
	}
	out := make([]msbSdk.PortBinding, len(bindings))
	for i, binding := range bindings {
		out[i] = msbSdk.PortBinding{
			Bind:      binding.Bind,
			HostPort:  binding.HostPort,
			GuestPort: binding.GuestPort,
			Protocol:  msbSdk.PortProtocol(binding.Protocol),
		}
	}
	return out
}

// secretEntries translates launcher secret entries, preserving nil as nil.
func secretEntries(entries []SecretEntry) []msbSdk.SecretEntry {
	if entries == nil {
		return nil
	}
	out := make([]msbSdk.SecretEntry, len(entries))
	for i, entry := range entries {
		out[i] = secretEntry(entry)
	}
	return out
}

// secretEntry rebuilds the SDK secret entry through the SDK's Secret.Env
// factory, which folds Passthrough into AllowPlaceholderFor + Passthrough.
func secretEntry(e SecretEntry) msbSdk.SecretEntry {
	return msbSdk.Secret.Env(e.EnvVar, e.Value, msbSdk.SecretEnvOptions{
		Allow:               e.Allow,
		AllowPlaceholderFor: e.Passthrough,
		Placeholder:         e.Placeholder,
		RequireTLSIdentity:  e.RequireTLSIdentity,
		Substitution: msbSdk.SecretSubstitution{
			Headers:      e.Substitution.Headers,
			HeaderFields: e.Substitution.HeaderFields,
			Query:        e.Substitution.Query,
			Body:         e.Substitution.Body,
		},
		ViolationAction: msbSdk.ViolationAction(e.ViolationAction),
	})
}

// networkConfig composes the SDK network policy from a launcher NetworkSpec.
// It reproduces the launcher's rule order: the gateway-DNS rule first only when
// a composable profile is present, then the egress-deny carve-outs, then the
// profile allow rules, then the explicit egress-allow rules (first match wins).
func networkConfig(spec NetworkSpec) (*msbSdk.NetworkConfig, error) {
	profiles := make([]msbSdk.NetworkProfile, 0, len(spec.Profiles))
	for _, profile := range spec.Profiles {
		profiles = append(profiles, msbSdk.NetworkProfile(profile))
	}
	cfg, err := msbSdk.NetworkPolicy.FromProfilesChecked(profiles...)
	if err != nil {
		return nil, err
	}
	// FromProfilesChecked returns the gateway-DNS rule first, then the profile
	// allow rules. Drop that DNS rule and re-emit it at the front so the deny
	// carve-outs can sit between them; no profile selects no rules at all.
	profileRules := cfg.Rules
	if len(profiles) > 0 {
		profileRules = cfg.Rules[1:]
	}
	if len(spec.DNSServers) > 0 {
		cfg.DNS = &msbSdk.DNSConfig{Nameservers: spec.DNSServers}
	}
	denyRules := make([]msbSdk.PolicyRule, 0, len(spec.EgressDeny))
	for _, destination := range spec.EgressDeny {
		denyRules = append(denyRules, networkEgressRule(msbSdk.PolicyActionDeny, destination))
	}
	allowRules := make([]msbSdk.PolicyRule, 0, len(spec.EgressAllow))
	for _, destination := range spec.EgressAllow {
		allowRules = append(allowRules, networkEgressRule(msbSdk.PolicyActionAllow, destination))
	}
	var gatewayDNSRules []msbSdk.PolicyRule
	if len(profiles) > 0 {
		gatewayDNSRules = []msbSdk.PolicyRule{msbSdk.Rule.AllowDNS()}
	}
	cfg.Rules = slices.Concat(gatewayDNSRules, denyRules, profileRules, allowRules)
	return cfg, nil
}

// networkEgressRule builds one explicit egress policy rule.
func networkEgressRule(action msbSdk.PolicyAction, destination string) msbSdk.PolicyRule {
	var rule msbSdk.PolicyRule
	rule.Action = action
	rule.Direction = msbSdk.PolicyDirectionEgress
	rule.Destination = destination
	return rule
}

// volumeOptions translates a VolumeSpec into the SDK creation options.
func volumeOptions(spec VolumeSpec) []msbSdk.VolumeOption {
	return []msbSdk.VolumeOption{msbSdk.WithVolumeKind(volumeKindTo(spec.Kind))}
}

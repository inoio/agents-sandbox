package msb

// Reflection covers the exported SDK structs a translator projects — both the
// option (input) structs and the return structs the phase-3 translators copy
// field for field. The SDK's jobExecConfig, jobListConfig, lifecycleOptions and
// destroyOptions are unexported and mutate unexported fields, so they cannot be
// reflected; drift there is caught only by the compiler when the adapters call
// the SDK constructors (Task 2.3).

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestSDKStructFieldsAreClassified(t *testing.T) {
	tests := []struct {
		name       string
		structType reflect.Type
		projected  map[string]struct{}
		notModeled map[string]string
	}{
		{"ExecConfig", reflect.TypeFor[msbSdk.ExecConfig](), execProjected, execNotModeled},
		{"AttachConfig", reflect.TypeFor[msbSdk.AttachConfig](), attachProjected, attachNotModeled},
		{"LogOptions", reflect.TypeFor[msbSdk.LogOptions](), logProjected, logNotModeled},
		{
			"DiskCompactionOptions",
			reflect.TypeFor[msbSdk.DiskCompactionOptions](),
			compactionProjected,
			compactionNotModeled,
		},
		{
			"StoragePruneOptions",
			reflect.TypeFor[msbSdk.StoragePruneOptions](),
			storagePruneProjected,
			storagePruneNotModeled,
		},
		{
			"JobLogOptions",
			reflect.TypeFor[msbSdk.JobLogOptions](),
			jobLogProjected,
			jobLogNotModeled,
		},
		{
			"StoragePruneReport",
			reflect.TypeFor[msbSdk.StoragePruneReport](),
			storagePruneReportProjected,
			storagePruneReportNotModeled,
		},
		{"JobInfo", reflect.TypeFor[msbSdk.JobInfo](), jobInfoProjected, jobInfoNotModeled},
		{"JobLogEntry", reflect.TypeFor[msbSdk.JobLogEntry](), jobLogEntryProjected, jobLogEntryNotModeled},
		{"LogEntry", reflect.TypeFor[msbSdk.LogEntry](), logEntryProjected, logEntryNotModeled},
		{
			"SandboxConfig",
			reflect.TypeFor[msbSdk.SandboxConfig](),
			sandboxConfigProjected,
			sandboxConfigNotModeled,
		},
		{
			"VolumeConfig",
			reflect.TypeFor[msbSdk.VolumeConfig](),
			volumeConfigProjected,
			volumeConfigNotModeled,
		},
		{
			"ModifyOptions",
			reflect.TypeFor[msbSdk.ModifyOptions](),
			modifyProjected,
			modifyNotModeled,
		},
		{
			"SandboxModificationPlan",
			reflect.TypeFor[msbSdk.SandboxModificationPlan](),
			sandboxModificationPlanProjected,
			sandboxModificationPlanNotModeled,
		},
		{
			"DiskCompactionResult",
			reflect.TypeFor[msbSdk.DiskCompactionResult](),
			diskCompactionResultProjected,
			diskCompactionResultNotModeled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkProjection(t, tt.structType, tt.projected, tt.notModeled)
		})
	}
}

// checkProjection asserts that every field of structType is classified exactly
// once: either projected or deliberately not modeled.
func checkProjection(
	t *testing.T,
	structType reflect.Type,
	projected map[string]struct{},
	notModeled map[string]string,
) {
	t.Helper()

	fields := make(map[string]struct{}, structType.NumField())
	for field := range structType.Fields() {
		fields[field.Name] = struct{}{}
	}

	var missing []string
	for name := range fields {
		if _, ok := projected[name]; ok {
			continue
		}
		if _, ok := notModeled[name]; ok {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("unclassified %s fields (add to projected or notModeled): %v", structType, missing)
	}

	for name := range projected {
		if _, ok := notModeled[name]; ok {
			t.Errorf("%s field %q is both projected and notModeled", structType, name)
		}
	}

	var extra []string
	for name := range projected {
		if _, ok := fields[name]; !ok {
			extra = append(extra, name)
		}
	}
	for name := range notModeled {
		if _, ok := fields[name]; !ok {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Errorf("classified names not present on %s: %v", structType, extra)
	}
}

func TestSandboxStatusFromTranslatesValues(t *testing.T) {
	tests := []struct {
		in   msbSdk.SandboxStatus
		want SandboxStatus
	}{
		{msbSdk.SandboxStatusCreated, SandboxStatusCreated},
		{msbSdk.SandboxStatusStarting, SandboxStatusStarting},
		{msbSdk.SandboxStatusRunning, SandboxStatusRunning},
		{msbSdk.SandboxStatusDraining, SandboxStatusDraining},
		{msbSdk.SandboxStatusPaused, SandboxStatusPaused},
		{msbSdk.SandboxStatusStopped, SandboxStatusStopped},
		{msbSdk.SandboxStatusCrashed, SandboxStatusCrashed},
		{msbSdk.SandboxStatus("future-status"), SandboxStatus("future-status")},
		{msbSdk.SandboxStatus(""), SandboxStatus("")},
	}
	for _, tt := range tests {
		if got := sandboxStatusFrom(tt.in); got != tt.want {
			t.Errorf("sandboxStatusFrom(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBackendKindFromTranslatesValues(t *testing.T) {
	tests := []struct {
		in   msbSdk.ContextKind
		want BackendKind
	}{
		{msbSdk.ContextLocal, BackendLocal},
		{msbSdk.ContextRemote, BackendCloud},
		{msbSdk.ContextKind(""), BackendKind("")},
		{msbSdk.ContextKind("future-backend"), BackendKind("future-backend")},
	}
	for _, tt := range tests {
		if got := backendKindFrom(tt.in); got != tt.want {
			t.Errorf("backendKindFrom(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestVolumeKindFromTranslatesValues(t *testing.T) {
	tests := []struct {
		in   msbSdk.VolumeKind
		want VolumeKind
	}{
		{msbSdk.VolumeKindDir, VolumeKindDir},
		{msbSdk.VolumeKindDisk, VolumeKindDisk},
		{msbSdk.VolumeKind(""), VolumeKind("")},
	}
	for _, tt := range tests {
		if got := volumeKindFrom(tt.in); got != tt.want {
			t.Errorf("volumeKindFrom(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExecOptionsProjectRequest(t *testing.T) {
	if opts := execOptions(ExecRequest{}); len(opts) != 0 {
		t.Fatalf("zero request produced %d options, want 0", len(opts))
	}

	request := ExecRequest{
		User:      "dev",
		Cwd:       "/workspace",
		Env:       map[string]string{"FOO": "bar"},
		TTY:       true,
		Timeout:   3 * time.Second,
		StdinPipe: true,
	}
	var cfg msbSdk.ExecConfig
	for _, opt := range execOptions(request) {
		opt(&cfg)
	}

	if cfg.User != "dev" {
		t.Errorf("User = %q, want dev", cfg.User)
	}
	if cfg.Cwd != "/workspace" {
		t.Errorf("Cwd = %q, want /workspace", cfg.Cwd)
	}
	if cfg.Env["FOO"] != "bar" {
		t.Errorf("Env = %v, want FOO=bar", cfg.Env)
	}
	if !cfg.TTY {
		t.Error("TTY = false, want true")
	}
	if cfg.Timeout != 3*time.Second {
		t.Errorf("Timeout = %v, want 3s", cfg.Timeout)
	}
	if !cfg.StdinPipe {
		t.Error("StdinPipe = false, want true")
	}
}

func TestExecOptionsOmitSDKDefaults(t *testing.T) {
	opts := execOptions(ExecRequest{User: "", Cwd: "", Env: nil, TTY: false, Timeout: 0, StdinPipe: false})
	if len(opts) != 0 {
		t.Fatalf("default-valued request produced %d options, want 0", len(opts))
	}
}

func TestAttachOptionsProjectRequest(t *testing.T) {
	if opts := attachOptions(AttachRequest{}); len(opts) != 0 {
		t.Fatalf("zero request produced %d options, want 0", len(opts))
	}

	request := AttachRequest{
		Cwd:        "/workspace",
		User:       "dev",
		Env:        map[string]string{"FOO": "bar"},
		DetachKeys: "ctrl-p,ctrl-q",
	}
	var cfg msbSdk.AttachConfig
	for _, opt := range attachOptions(request) {
		opt(&cfg)
	}

	if cfg.Cwd != "/workspace" {
		t.Errorf("Cwd = %q, want /workspace", cfg.Cwd)
	}
	if cfg.User != "dev" {
		t.Errorf("User = %q, want dev", cfg.User)
	}
	if cfg.Env["FOO"] != "bar" {
		t.Errorf("Env = %v, want FOO=bar", cfg.Env)
	}
	if cfg.DetachKeys != "ctrl-p,ctrl-q" {
		t.Errorf("DetachKeys = %q, want ctrl-p,ctrl-q", cfg.DetachKeys)
	}
}

func TestAttachOptionsOmitSDKDefaults(t *testing.T) {
	if opts := attachOptions(AttachRequest{}); len(opts) != 0 {
		t.Fatalf("default-valued request produced %d options, want 0", len(opts))
	}
}

func TestJobExecOptionsReuseExecOptions(t *testing.T) {
	if opts := jobExecOptions(JobSpec{}); len(opts) != 0 {
		t.Fatalf("zero job spec produced %d options, want 0", len(opts))
	}

	spec := JobSpec{ExecRequest{User: "dev", Cwd: "/workspace"}}
	if opts := jobExecOptions(spec); len(opts) != 2 {
		t.Fatalf("job spec produced %d options, want 2", len(opts))
	}
}

func TestJobListOptionsProjectQuery(t *testing.T) {
	if opts := jobListOptions(JobQuery{}); len(opts) != 0 {
		t.Fatalf("zero query produced %d options, want 0", len(opts))
	}

	query := JobQuery{All: true, Limit: 10, Cursor: "next"}
	if opts := jobListOptions(query); len(opts) != 3 {
		t.Fatalf("query produced %d options, want 3", len(opts))
	}
}

func TestLifecycleOptionsOmitZeroTimeout(t *testing.T) {
	stop, kill, destroy := lifecycleOptions(LifecycleOptions{})
	if len(stop) != 0 || len(kill) != 0 || len(destroy) != 0 {
		t.Fatalf("zero options produced stop=%d kill=%d destroy=%d, want 0", len(stop), len(kill), len(destroy))
	}
}

func TestLifecycleOptionsProjectTimeoutAndForce(t *testing.T) {
	stop, kill, destroy := lifecycleOptions(LifecycleOptions{Timeout: 5 * time.Second})
	if len(stop) != 1 {
		t.Errorf("Stop options = %d, want 1", len(stop))
	}
	if len(kill) != 1 {
		t.Errorf("Kill options = %d, want 1", len(kill))
	}
	if len(destroy) != 1 {
		t.Errorf("Destroy options = %d, want 1 (timeout only)", len(destroy))
	}

	_, _, destroy = lifecycleOptions(LifecycleOptions{Timeout: 5 * time.Second, Force: true})
	if len(destroy) != 2 {
		t.Errorf("Destroy options = %d, want 2 (timeout + force)", len(destroy))
	}

	stop, kill, destroy = lifecycleOptions(LifecycleOptions{Force: true})
	if len(stop) != 0 || len(kill) != 0 {
		t.Errorf("force-only stop=%d kill=%d, want 0", len(stop), len(kill))
	}
	if len(destroy) != 1 {
		t.Errorf("force-only destroy = %d, want 1", len(destroy))
	}
}

func TestLogOptionsProjectQuery(t *testing.T) {
	if got := logOptions(LogQuery{}); !reflect.DeepEqual(got, msbSdk.LogOptions{}) {
		t.Errorf("zero query = %+v, want zero LogOptions", got)
	}

	query := LogQuery{
		Tail:    100,
		Since:   time.Unix(10, 0),
		Until:   time.Unix(20, 0),
		Sources: []LogSource{LogSourceStdout, LogSourceSystem},
	}
	got := logOptions(query)
	want := msbSdk.LogOptions{
		Tail:    100,
		Since:   time.Unix(10, 0),
		Until:   time.Unix(20, 0),
		Sources: []msbSdk.LogSource{msbSdk.LogSourceStdout, msbSdk.LogSourceSystem},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("logOptions() = %+v, want %+v", got, want)
	}
}

func TestLogSourcesMapsNilAndValues(t *testing.T) {
	if got := logSources(nil); got != nil {
		t.Errorf("logSources(nil) = %v, want nil", got)
	}

	got := logSources([]LogSource{LogSourceStdout, LogSourceSystem})
	want := []msbSdk.LogSource{msbSdk.LogSourceStdout, msbSdk.LogSourceSystem}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("logSources() = %v, want %v", got, want)
	}
}

func TestCompactionOptionsProjectRequest(t *testing.T) {
	if got := compactionOptions(CompactionRequest{}); !reflect.DeepEqual(got, msbSdk.DiskCompactionOptions{}) {
		t.Errorf("zero request = %+v, want zero DiskCompactionOptions", got)
	}

	layers := uint32(4)
	request := CompactionRequest{Layers: &layers, DryRun: true, Disk: "/", RootDiskOnly: true}
	want := msbSdk.DiskCompactionOptions{Layers: &layers, DryRun: true, Disk: "/", RootDiskOnly: true}
	if got := compactionOptions(request); !reflect.DeepEqual(got, want) {
		t.Errorf("compactionOptions() = %+v, want %+v", got, want)
	}
}

func TestStoragePruneOptionsProjectRequest(t *testing.T) {
	if got := storagePruneOptions(PruneRequest{}); !reflect.DeepEqual(got, msbSdk.StoragePruneOptions{}) {
		t.Errorf("zero request = %+v, want zero StoragePruneOptions", got)
	}

	request := PruneRequest{DryRun: true, OlderThanSeconds: 3600}
	want := msbSdk.StoragePruneOptions{DryRun: true, OlderThanSeconds: 3600}
	if got := storagePruneOptions(request); !reflect.DeepEqual(got, want) {
		t.Errorf("storagePruneOptions() = %+v, want %+v", got, want)
	}
}

func TestPruneReportFromProjectsTotals(t *testing.T) {
	physical := uint64(4096)
	report := pruneReportFrom(&msbSdk.StoragePruneReport{
		DryRun:                 true,
		FilesRemoved:           3,
		LogicalBytesRemoved:    12288,
		PhysicalBytesReclaimed: &physical,
		Truncated:              true,
	})
	if report == nil {
		t.Fatal("pruneReportFrom() = nil, want a report")
	}
	if report.FilesRemoved != 3 || report.LogicalBytesRemoved != 12288 ||
		report.PhysicalBytesReclaimed != &physical {
		t.Errorf("pruneReportFrom() = %+v, want files 3 logical 12288 physical %d", report, physical)
	}
}

func TestPruneReportFromNilIsNil(t *testing.T) {
	if got := pruneReportFrom(nil); got != nil {
		t.Errorf("pruneReportFrom(nil) = %v, want nil", got)
	}
}

func TestJobLogOptionsProjectQuery(t *testing.T) {
	if got := jobLogOptions(JobLogQuery{}); !reflect.DeepEqual(got, msbSdk.JobLogOptions{}) {
		t.Errorf("zero query = %+v, want zero JobLogOptions", got)
	}

	tail := uint(100)
	since := int64(10)
	until := int64(20)
	query := JobLogQuery{
		Tail:       &tail,
		Since:      &since,
		Until:      &until,
		Sources:    []string{"stdout", "system"},
		FromCursor: "abc",
		Follow:     true,
	}
	want := msbSdk.JobLogOptions{
		Tail:       &tail,
		Since:      &since,
		Until:      &until,
		Sources:    []msbSdk.JobOutputSource{msbSdk.JobOutputStdout, msbSdk.JobOutputSource("system")},
		FromCursor: "abc",
		Follow:     true,
	}
	if got := jobLogOptions(query); !reflect.DeepEqual(got, want) {
		t.Errorf("jobLogOptions() = %+v, want %+v", got, want)
	}
}

func TestJobInfoFromProjectsFields(t *testing.T) {
	pid := uint32(42)
	code := 7
	startedAt := int64(1000)
	finishedAt := int64(2000)
	message := "boom"
	info := jobInfoFrom(&msbSdk.JobInfo{
		ID:            "job-1",
		RuntimeBootID: "boot-1",
		Command:       []string{"dockerd"},
		State:         "exited",
		TTY:           true,
		StdinClosed:   true,
		PID:           &pid,
		CreatedAt:     500,
		StartedAt:     &startedAt,
		FinishedAt:    &finishedAt,
		ExitCode:      &code,
		TimedOut:      true,
		Failure:       &msbSdk.ExecFailure{Kind: "spawn", Message: "no such file", Path: "/bin/x"},
		Error:         &message,
	})
	if info == nil {
		t.Fatal("jobInfoFrom() = nil, want info")
	}
	if info.ID != "job-1" || info.RuntimeBootID != "boot-1" || info.State != "exited" ||
		!info.TTY || !info.StdinClosed || info.PID != &pid || info.CreatedAt != 500 ||
		info.StartedAt != &startedAt || info.FinishedAt != &finishedAt ||
		info.ExitCode != &code || !info.TimedOut || info.Error != &message {
		t.Errorf("jobInfoFrom() = %+v, want every scalar field projected", info)
	}
	if len(info.Command) != 1 || info.Command[0] != "dockerd" {
		t.Errorf("Command = %v, want [dockerd]", info.Command)
	}
	if info.Failure == nil || info.Failure.Kind != "spawn" || info.Failure.Message != "no such file" ||
		info.Failure.Path != "/bin/x" {
		t.Errorf("Failure = %+v, want the projected failure", info.Failure)
	}
}

func TestJobInfoFromNilIsNil(t *testing.T) {
	if got := jobInfoFrom(nil); got != nil {
		t.Errorf("jobInfoFrom(nil) = %v, want nil", got)
	}
}

func TestJobInfosFromPreservesNil(t *testing.T) {
	if got := jobInfosFrom(nil); got != nil {
		t.Errorf("jobInfosFrom(nil) = %v, want nil", got)
	}
	got := jobInfosFrom([]msbSdk.JobInfo{{ID: "a"}, {ID: "b"}})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("jobInfosFrom() = %+v, want two entries", got)
	}
}

func TestJobLogEntriesFromPreservesNilAndFields(t *testing.T) {
	if got := jobLogEntriesFrom(nil); got != nil {
		t.Errorf("jobLogEntriesFrom(nil) = %v, want nil", got)
	}
	got := jobLogEntriesFrom([]msbSdk.JobLogEntry{{
		Timestamp: 5,
		Source:    "stdout",
		Data:      []byte("hi"),
		Cursor:    "c1",
	}})
	if len(got) != 1 || got[0].Timestamp != 5 || got[0].Source != "stdout" ||
		string(got[0].Data) != "hi" || got[0].Cursor != "c1" {
		t.Errorf("jobLogEntriesFrom() = %+v, want every field projected", got)
	}
}

func TestLogEntriesFromPreservesNilAndFields(t *testing.T) {
	if got := logEntriesFrom(nil); got != nil {
		t.Errorf("logEntriesFrom(nil) = %v, want nil", got)
	}
	session := uint64(9)
	got := logEntriesFrom([]msbSdk.LogEntry{{
		Source:    msbSdk.LogSourceSystem,
		SessionID: &session,
		Timestamp: time.Unix(3, 0),
		Data:      []byte("oom"),
		Cursor:    "c2",
	}})
	if len(got) != 1 || got[0].Source != LogSourceSystem || got[0].SessionID != &session ||
		!got[0].Timestamp.Equal(time.Unix(3, 0)) || string(got[0].Data) != "oom" || got[0].Cursor != "c2" {
		t.Errorf("logEntriesFrom() = %+v, want every field projected", got)
	}
}

func TestSandboxOptionsProjectSpec(t *testing.T) {
	spec := SandboxSpec{
		Image:        "img:1",
		CPUs:         2,
		MaxCPUs:      4,
		MemoryMiB:    1024,
		MaxMemoryMiB: 4096,
		RootDiskMiB:  16 * 1024,
		Workdir:      "/workspace",
		Detached:     true,
		IdleTimeout:  5 * time.Minute,
		Env:          map[string]string{"A": "1"},
		Labels:       map[string]string{"k": "v"},
		Mounts: map[string]MountConfig{
			"/data": Mount.Bind("/host", MountOptions{Readonly: true}),
		},
		Ports: []PortBinding{{
			Bind:      "127.0.0.1",
			HostPort:  3000,
			GuestPort: 8080,
			Protocol:  PortProtocolTCP,
		}},
		Secrets: []SecretEntry{Secret.Env("TOKEN", "s", SecretEnvOptions{Allow: []string{"api"}})},
		Network: NetworkSpec{Profiles: []NetworkProfile{NetworkProfilePublic}},
	}
	opts, err := sandboxOptions(spec)
	if err != nil {
		t.Fatalf("sandboxOptions: %v", err)
	}
	var cfg msbSdk.SandboxConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.Image != "img:1" {
		t.Errorf("Image = %q, want img:1", cfg.Image)
	}
	if cfg.CPUs != 2 || cfg.MaxCPUs != 4 || cfg.MemoryMiB != 1024 || cfg.MaxMemoryMiB != 4096 {
		t.Errorf("resources = cpus %d/%d mem %d/%d, want 2/4 1024/4096",
			cfg.CPUs, cfg.MaxCPUs, cfg.MemoryMiB, cfg.MaxMemoryMiB)
	}
	if cfg.Workdir != "/workspace" || !cfg.Detached || cfg.IdleTimeout != 5*time.Minute {
		t.Errorf("workdir/detached/idle = %q/%v/%v, want /workspace/true/5m",
			cfg.Workdir, cfg.Detached, cfg.IdleTimeout)
	}
	if cfg.Env["A"] != "1" {
		t.Errorf("Env = %v, want A=1", cfg.Env)
	}
	if cfg.Labels["k"] != "v" {
		t.Errorf("Labels = %v, want k=v", cfg.Labels)
	}
	mount, ok := cfg.Volumes["/data"]
	if !ok || mount.Kind() != msbSdk.MountKindBind || mount.Bind != "/host" || !mount.Readonly {
		t.Errorf("Volumes[/data] = %+v, want a readonly bind of /host", mount)
	}
	if len(cfg.PortBindings) != 1 || cfg.PortBindings[0].HostPort != 3000 ||
		cfg.PortBindings[0].GuestPort != 8080 || cfg.PortBindings[0].Protocol != msbSdk.PortProtocolTCP {
		t.Errorf("PortBindings = %+v, want 127.0.0.1:3000 → 8080/tcp", cfg.PortBindings)
	}
	if len(cfg.Secrets) != 1 || cfg.Secrets[0].EnvVar != "TOKEN" || cfg.Secrets[0].Value != "s" {
		t.Errorf("Secrets = %+v, want the TOKEN secret", cfg.Secrets)
	}
	if cfg.Network == nil || cfg.Network.DefaultEgress != msbSdk.PolicyActionDeny {
		t.Errorf("Network = %+v, want deny-by-default egress", cfg.Network)
	}
	if cfg.RootDisk == nil || cfg.RootDisk.Kind() != msbSdk.RootDiskKindManaged ||
		cfg.RootDisk.SizeMiB != 16*1024 {
		t.Errorf("RootDisk = %+v, want a managed 16 GiB disk", cfg.RootDisk)
	}
}

func TestSandboxOptionsOmitRootDiskWhenUnset(t *testing.T) {
	opts, err := sandboxOptions(SandboxSpec{})
	if err != nil {
		t.Fatalf("sandboxOptions: %v", err)
	}
	var cfg msbSdk.SandboxConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.RootDisk != nil {
		t.Errorf("RootDisk = %+v, want none without a root disk size", cfg.RootDisk)
	}
	if len(cfg.PortBindings) != 0 {
		t.Errorf("PortBindings = %v, want none", cfg.PortBindings)
	}
	// An omitted option leaves the map nil; emitting it with an empty value
	// would allocate an empty map the runtime then persists.
	if cfg.Labels != nil || cfg.Volumes != nil || cfg.Env != nil {
		t.Errorf("default-valued spec allocated an empty map: labels=%v volumes=%v env=%v",
			cfg.Labels, cfg.Volumes, cfg.Env)
	}
}

func TestSandboxOptionsOmitSDKDefaults(t *testing.T) {
	// A throwaway task sandbox sets only image, labels and mounts; the adapter
	// must not detach, bound, size, or publish it.
	opts, err := sandboxOptions(SandboxSpec{
		Image:  "img:1",
		Labels: map[string]string{"k": "v"},
		Mounts: map[string]MountConfig{"/data": Mount.Named("vol", MountOptions{})},
	})
	if err != nil {
		t.Fatalf("sandboxOptions: %v", err)
	}
	var cfg msbSdk.SandboxConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Detached {
		t.Error("Detached = true for a default-valued spec, want false")
	}
	if cfg.CPUs != 0 || cfg.MaxCPUs != 0 || cfg.MemoryMiB != 0 || cfg.MaxMemoryMiB != 0 ||
		cfg.IdleTimeout != 0 || cfg.Workdir != "" || cfg.RootDisk != nil {
		t.Errorf("default-valued spec set a resource: %+v", cfg)
	}
	if len(cfg.PortBindings) != 0 || len(cfg.Secrets) != 0 || len(cfg.Env) != 0 {
		t.Errorf("default-valued spec set a collection: %+v", cfg)
	}
}

func TestSandboxOptionsPropagateNetworkError(t *testing.T) {
	_, err := sandboxOptions(SandboxSpec{
		Network: NetworkSpec{Profiles: []NetworkProfile{NetworkProfile("bogus")}},
	})
	if err == nil {
		t.Fatal("sandboxOptions() = nil error, want a rejected profile")
	}
}

func TestSandboxOptionsProjectProxy(t *testing.T) {
	spec := SandboxSpec{Image: "alpine:3.19", Network: NetworkSpec{
		Proxy: ProxySpec{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "PW"},
	}}
	opts, err := sandboxOptions(spec)
	if err != nil {
		t.Fatal(err)
	}
	var cfg msbSdk.SandboxConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	want := msbSdk.SOCKS5Proxy("10.0.0.1:1080").Credentials("u", msbSdk.SecretSourceEnv("PW"))
	if !reflect.DeepEqual(cfg.Proxy, want) {
		t.Fatalf("Proxy = %+v, want the SDK-built %+v", cfg.Proxy, want)
	}
}

// TestSandboxOptionsOmitProxyWhenUnset pins that a spec with no proxy emits no
// proxy option, so a proxy-less sandbox is not routed through an empty proxy.
func TestSandboxOptionsOmitProxyWhenUnset(t *testing.T) {
	opts, err := sandboxOptions(SandboxSpec{Image: "alpine:3.19"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg msbSdk.SandboxConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Proxy != nil {
		t.Fatalf("Proxy = %+v, want nil without a configured proxy", cfg.Proxy)
	}
}

// TestSandboxOptionsProjectProxyProtocols pins that every supported protocol
// reaches the SDK through its protocol-specific constructor.
func TestSandboxOptionsProjectProxyProtocols(t *testing.T) {
	for _, tc := range []struct {
		protocol ProxyProtocol
		want     *msbSdk.OutboundProxy
	}{
		{ProxyHTTPConnect, msbSdk.HTTPConnectProxy("10.0.0.1:1080")},
		{ProxySOCKS5, msbSdk.SOCKS5Proxy("10.0.0.1:1080")},
		{ProxySOCKS4, msbSdk.SOCKS4Proxy("10.0.0.1:1080")},
	} {
		t.Run(string(tc.protocol), func(t *testing.T) {
			opts, err := sandboxOptions(SandboxSpec{
				Image:   "alpine:3.19",
				Network: NetworkSpec{Proxy: ProxySpec{Protocol: tc.protocol, Address: "10.0.0.1:1080"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var cfg msbSdk.SandboxConfig
			for _, opt := range opts {
				opt(&cfg)
			}
			if !reflect.DeepEqual(cfg.Proxy, tc.want) {
				t.Fatalf("protocol %q projected %+v, want the SDK-built %+v", tc.protocol, cfg.Proxy, tc.want)
			}
		})
	}
}

// TestSandboxOptionsRejectUnknownProxyProtocol pins that an unknown protocol
// fails the translation rather than silently dropping the proxy, and that the
// message carries the protocol hint shared with the launcher's parser.
func TestSandboxOptionsRejectUnknownProxyProtocol(t *testing.T) {
	_, err := sandboxOptions(SandboxSpec{
		Image:   "alpine:3.19",
		Network: NetworkSpec{Proxy: ProxySpec{Protocol: ProxyProtocol("ftp"), Address: "10.0.0.1:1080"}},
	})
	if err == nil {
		t.Fatal("an unknown proxy protocol must fail the translation")
	}
	want := `unknown proxy protocol "ftp" (want ` + ProxyProtocolsHint + `)`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// TestSandboxOptionsRejectLoneProxyCredential pins that sandboxProxyOption is
// symmetric with the launcher's Proxy.validate: exactly one of username and
// password-env fails the translation instead of reaching the VM anonymous.
func TestSandboxOptionsRejectLoneProxyCredential(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proxy ProxySpec
	}{
		{"username only", ProxySpec{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u"}},
		{"password-env only", ProxySpec{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", PasswordEnv: "P"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sandboxOptions(SandboxSpec{Image: "alpine:3.19", Network: NetworkSpec{Proxy: tc.proxy}})
			if err == nil {
				t.Fatal("a lone proxy credential must fail the translation")
			}
			if err.Error() != "username and password-env must be set together" {
				t.Fatalf("error = %q, want the lone-credential rejection", err)
			}
		})
	}
}

func TestNetworkConfigRuleOrder(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{
		Profiles:    []NetworkProfile{NetworkProfilePublic},
		EgressDeny:  []string{"deny.example"},
		EgressAllow: []string{"allow.example"},
		DNSServers:  []string{"1.1.1.1:53"},
	})
	if err != nil {
		t.Fatalf("networkConfig: %v", err)
	}
	if cfg.DefaultEgress != msbSdk.PolicyActionDeny || cfg.DefaultIngress != msbSdk.PolicyActionAllow {
		t.Errorf("defaults = %q/%q, want deny/allow", cfg.DefaultEgress, cfg.DefaultIngress)
	}
	wantActions := []msbSdk.PolicyAction{
		msbSdk.PolicyActionAllow, // gateway DNS
		msbSdk.PolicyActionDeny,  // egress-deny carve-out
		msbSdk.PolicyActionAllow, // profile allow
		msbSdk.PolicyActionAllow, // explicit allow
	}
	if len(cfg.Rules) != len(wantActions) {
		t.Fatalf("rules = %+v, want %d in gateway-DNS → deny → profile → allow order", cfg.Rules, len(wantActions))
	}
	for i, want := range wantActions {
		if cfg.Rules[i].Action != want {
			t.Errorf("rule[%d].Action = %q, want %q (%+v)", i, cfg.Rules[i].Action, want, cfg.Rules)
		}
		if cfg.Rules[i].Direction != msbSdk.PolicyDirectionEgress {
			t.Errorf("rule[%d].Direction = %q, want egress (%+v)", i, cfg.Rules[i].Direction, cfg.Rules)
		}
	}
	if cfg.Rules[0].Destination != "host" || cfg.Rules[0].Port != "53" {
		t.Errorf("rule[0] = %+v, want the gateway-DNS rule first", cfg.Rules[0])
	}
	if cfg.Rules[1].Destination != "deny.example" || cfg.Rules[2].Destination != "public" ||
		cfg.Rules[3].Destination != "allow.example" {
		t.Errorf("rule destinations = %q/%q/%q, want deny.example/public/allow.example",
			cfg.Rules[1].Destination, cfg.Rules[2].Destination, cfg.Rules[3].Destination)
	}
	if cfg.DNS == nil || len(cfg.DNS.Nameservers) != 1 || cfg.DNS.Nameservers[0] != "1.1.1.1:53" {
		t.Errorf("DNS = %+v, want 1.1.1.1:53", cfg.DNS)
	}
}

func TestNetworkConfigNoneOmitsGatewayDNS(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{})
	if err != nil {
		t.Fatalf("networkConfig: %v", err)
	}
	if len(cfg.Rules) != 0 {
		t.Errorf("rules = %+v, want none without a profile", cfg.Rules)
	}
	if cfg.DNS != nil {
		t.Errorf("DNS = %+v, want nil without nameservers", cfg.DNS)
	}
}

func TestNetworkConfigSetsTLSForInterceptOn(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{InterceptTLS: InterceptOn, TLSBypass: []string{"*.internal"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS == nil || len(cfg.TLS.Bypass) != 1 {
		t.Fatalf("want TLS with bypass, got %+v", cfg.TLS)
	}
}

func TestNetworkConfigSetsTLSForInterceptOnWithoutBypass(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{InterceptTLS: InterceptOn})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS == nil || len(cfg.TLS.Bypass) != 0 {
		t.Fatalf("on must emit interception even with no bypass, got %+v", cfg.TLS)
	}
}

func TestNetworkConfigLeavesTLSNilForAutoWithoutBypass(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS != nil {
		t.Fatal("auto with no bypass must not set TLS")
	}
}

func TestNetworkConfigLeavesTLSNilForInterceptOff(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{InterceptTLS: InterceptOff})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS != nil {
		t.Fatal("off must not set TLS")
	}
}

func TestNetworkConfigSetsTLSForBypassUnderAuto(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{TLSBypass: []string{"*.internal"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS == nil || len(cfg.TLS.Bypass) != 1 {
		t.Fatalf("want TLS with bypass, got %+v", cfg.TLS)
	}
}

func TestNetworkConfigSetsUpstreamCACerts(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{UpstreamCACerts: []string{"PEM"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS == nil || len(cfg.TLS.UpstreamCACerts) != 1 {
		t.Fatalf("want upstream CA, got %+v", cfg.TLS)
	}
}

func TestNetworkConfigSetsTLSForUpstreamCACertsWithoutBypass(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{UpstreamCACerts: []string{"/ca.pem"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS == nil {
		t.Fatal("a non-empty upstream CA list must emit TLS even with no bypass")
	}
	if len(cfg.TLS.Bypass) != 0 {
		t.Fatalf("bypass = %v, want none", cfg.TLS.Bypass)
	}
	if len(cfg.TLS.UpstreamCACerts) != 1 {
		t.Fatalf("upstream CA = %v, want one entry", cfg.TLS.UpstreamCACerts)
	}
}

func TestNetworkConfigSetsDisableStrict(t *testing.T) {
	cfg, err := networkConfig(NetworkSpec{DisableStrict: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DisableStrict {
		t.Fatal("disable-strict must reach the SDK config")
	}
}

func TestSandboxOptionsRejectsOffWithSecrets(t *testing.T) {
	_, err := sandboxOptions(SandboxSpec{
		Image:   "img",
		Network: NetworkSpec{InterceptTLS: InterceptOff},
		Secrets: []SecretEntry{{EnvVar: "TOKEN", Value: "v"}},
	})
	if err == nil {
		t.Fatal("off with a configured secret must fail with an actionable error")
	}
	if msg := err.Error(); !strings.Contains(msg, "intercept-tls") ||
		!strings.Contains(msg, "auto/on") {
		t.Fatalf("error must name the intercept-tls limitation and the auto/on remedy, got %q", msg)
	}
	if _, err := sandboxOptions(
		SandboxSpec{Image: "img", Network: NetworkSpec{InterceptTLS: InterceptOff}},
	); err != nil {
		t.Fatalf("off without secrets must be accepted, got %v", err)
	}
}

func TestMountConfigTranslatesEachKind(t *testing.T) {
	owner := &MountOwner{UID: 1000, GID: 1000}
	tests := []struct {
		name  string
		mount MountConfig
		want  msbSdk.MountKind
	}{
		{
			"bind",
			Mount.Bind("/host", MountOptions{Readonly: true, QuotaMiB: 128, Owner: owner}),
			msbSdk.MountKindBind,
		},
		{"named", Mount.Named("vol", MountOptions{}), msbSdk.MountKindNamed},
		{"tmpfs", Mount.Tmpfs(TmpfsOptions{SizeMiB: 256}), msbSdk.MountKindTmpfs},
		{
			"disk",
			MountConfig{Kind: MountKindDisk, Disk: "/data.img", Format: "raw", Fstype: "ext4"},
			msbSdk.MountKindDisk,
		},
		{
			"owned",
			MountConfig{Kind: MountKindOwned, Owned: string(VolumeKindDir)},
			msbSdk.MountKindOwned,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mountConfig(tt.mount)
			if got.Kind() != tt.want {
				t.Errorf("mountConfig(%s).Kind() = %v, want %v", tt.name, got.Kind(), tt.want)
			}
		})
	}

	bind := mountConfig(Mount.Bind("/host", MountOptions{Readonly: true, QuotaMiB: 128, Owner: owner}))
	if bind.Bind != "/host" || !bind.Readonly || bind.QuotaMiB != 128 ||
		bind.Owner == nil || bind.Owner.UID != 1000 {
		t.Errorf("bind = %+v, want every option projected", bind)
	}
	named := mountConfig(Mount.Named("vol", MountOptions{}))
	if named.Named != "vol" {
		t.Errorf("named.Named = %q, want vol", named.Named)
	}
	tmpfs := mountConfig(Mount.Tmpfs(TmpfsOptions{SizeMiB: 256}))
	if !tmpfs.Tmpfs || tmpfs.SizeMiB != 256 {
		t.Errorf("tmpfs = %+v, want tmpfs 256 MiB", tmpfs)
	}
	disk := mountConfig(MountConfig{Kind: MountKindDisk, Disk: "/data.img", Format: "raw", Fstype: "ext4"})
	if disk.Disk != "/data.img" || disk.Format != "raw" || disk.Fstype != "ext4" {
		t.Errorf("disk = %+v, want /data.img raw ext4", disk)
	}
	owned := mountConfig(MountConfig{Kind: MountKindOwned, Owned: string(VolumeKindDisk), SizeMiB: 512})
	if owned.Owned != string(msbSdk.VolumeKindDisk) || owned.SizeMiB != 512 {
		t.Errorf("owned = %+v, want a disk-owned mount of 512 MiB", owned)
	}
}

func TestMountConfigsPreservesNil(t *testing.T) {
	if got := mountConfigs(nil); got != nil {
		t.Errorf("mountConfigs(nil) = %v, want nil", got)
	}
}

func TestPortBindingsPreservesNil(t *testing.T) {
	if got := portBindings(nil); got != nil {
		t.Errorf("portBindings(nil) = %v, want nil", got)
	}
}

func TestMountConfigUnknownKindIsZero(t *testing.T) {
	if got := mountConfig(MountConfig{}); got != (msbSdk.MountConfig{}) {
		t.Errorf("mountConfig(unknown kind) = %+v, want the zero SDK mount", got)
	}
}

func TestSecretEntryTranslatesFields(t *testing.T) {
	if got := secretEntries(nil); got != nil {
		t.Errorf("secretEntries(nil) = %v, want nil", got)
	}
	headers := true
	requireTLS := false
	entry := secretEntry(SecretEntry{
		EnvVar:             "TOKEN",
		Value:              "s",
		Allow:              []string{"api"},
		Passthrough:        []string{"proxy"},
		Placeholder:        "ph",
		RequireTLSIdentity: &requireTLS,
		Substitution: SecretSubstitution{
			Headers:      &headers,
			HeaderFields: []string{"authorization"},
			Query:        true,
			Body:         true,
		},
		ViolationAction: ViolationActionBlock,
	})
	if entry.EnvVar != "TOKEN" || entry.Value != "s" ||
		len(entry.Allow) != 1 || entry.Allow[0] != "api" ||
		len(entry.Passthrough) != 1 || entry.Passthrough[0] != "proxy" ||
		entry.Placeholder != "ph" ||
		entry.RequireTLSIdentity == nil || *entry.RequireTLSIdentity != false ||
		entry.ViolationAction != msbSdk.ViolationActionBlock {
		t.Errorf("secretEntry = %+v, want every field projected", entry)
	}
	if entry.Substitution.Headers == nil || !*entry.Substitution.Headers ||
		len(entry.Substitution.HeaderFields) != 1 || entry.Substitution.HeaderFields[0] != "authorization" ||
		!entry.Substitution.Query || !entry.Substitution.Body {
		t.Errorf("Substitution = %+v, want every field projected", entry.Substitution)
	}
}

func TestVolumeOptionsProjectKind(t *testing.T) {
	var cfg msbSdk.VolumeConfig
	for _, opt := range volumeOptions(VolumeSpec{Kind: VolumeKindDir}) {
		opt(&cfg)
	}
	if cfg.Kind != msbSdk.VolumeKindDir {
		t.Errorf("Kind = %q, want dir", cfg.Kind)
	}
}

func TestSandboxConfigFromProjectsFields(t *testing.T) {
	cpus := uint8(2)
	maxCPUs := uint8(4)
	maxMemoryMiB := uint32(2048)
	config := sandboxConfigFrom(&msbSdk.SandboxConfigInspection{
		Kind: "local",
		Spec: msbSdk.SandboxInspectionSpec{
			Image:  json.RawMessage(`{"Oci":{"reference":"img:1","root_disk":{"kind":"managed","size_mib":16384}}}`),
			Labels: map[string]string{"k": "v"},
			Resources: msbSdk.SandboxResourceInspection{
				CPUs:         &cpus,
				MaxCPUs:      &maxCPUs,
				MemoryMiB:    1024,
				MaxMemoryMiB: &maxMemoryMiB,
			},
		},
	})
	if config == nil {
		t.Fatal("sandboxConfigFrom() = nil, want a config")
	}
	if config.Image != "img:1" || config.CPUs != 2 || config.MaxCPUs != 4 ||
		config.MemoryMiB != 1024 || config.MaxMemoryMiB != 2048 {
		t.Errorf("sandboxConfigFrom() = %+v, want every scalar field projected", config)
	}
	if config.Labels["k"] != "v" {
		t.Errorf("Labels = %v, want k=v", config.Labels)
	}
	if config.RootDisk == nil || config.RootDisk.SizeMiB != 16384 {
		t.Errorf("RootDisk = %+v, want 16384 MiB", config.RootDisk)
	}
}

func TestSandboxConfigFromUnavailableResourcesAreZero(t *testing.T) {
	config := sandboxConfigFrom(&msbSdk.SandboxConfigInspection{
		Spec: msbSdk.SandboxInspectionSpec{
			Image: json.RawMessage(`{"Oci":{"reference":"img:1"}}`),
		},
	})
	if config == nil {
		t.Fatal("sandboxConfigFrom() = nil, want a config")
	}
	if config.CPUs != 0 || config.MaxCPUs != 0 || config.MaxMemoryMiB != 0 {
		t.Errorf("sandboxConfigFrom() = %+v, want zero for unavailable resources", config)
	}
	if config.RootDisk != nil {
		t.Errorf("RootDisk = %+v, want nil when the source records none", config.RootDisk)
	}
}

func TestSandboxConfigFromNilIsNil(t *testing.T) {
	if got := sandboxConfigFrom(nil); got != nil {
		t.Errorf("sandboxConfigFrom(nil) = %v, want nil", got)
	}
}

// TestDecodeInspectionRootfs covers the persisted RootfsSource forms the
// inspection reports: a tagged OCI source (with and without a root disk), a bind
// or disk-image path, a plain string, and the empty and malformed shapes a
// read-back must tolerate.
func TestDecodeInspectionRootfs(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantImage    string
		wantSizeMiB  uint32
		wantRootDisk bool
	}{
		{
			name:         "oci with managed root disk",
			raw:          `{"Oci":{"reference":"alpine:3.19","root_disk":{"kind":"managed","size_mib":8192}}}`,
			wantImage:    "alpine:3.19",
			wantSizeMiB:  8192,
			wantRootDisk: true,
		},
		{
			name:      "oci without root disk",
			raw:       `{"Oci":{"reference":"alpine:3.19"}}`,
			wantImage: "alpine:3.19",
		},
		{
			name:         "oci with an unsized managed root disk",
			raw:          `{"Oci":{"reference":"alpine:3.19","root_disk":{"kind":"managed"}}}`,
			wantImage:    "alpine:3.19",
			wantRootDisk: true,
		},
		{name: "bind", raw: `{"Bind":{"path":"/host/root"}}`, wantImage: "/host/root"},
		{name: "disk image", raw: `{"DiskImage":{"path":"/host/disk.raw"}}`, wantImage: "/host/disk.raw"},
		{name: "plain string", raw: `"alpine:3.19"`, wantImage: "alpine:3.19"},
		{name: "null", raw: `null`},
		{name: "empty"},
		{name: "malformed", raw: `{`},
		{name: "unknown variant", raw: `{"Future":{"reference":"x"}}`},
		{name: "oci not an object", raw: `{"Oci":5}`},
		{name: "bind not an object", raw: `{"Bind":5}`},
		{name: "disk image not an object", raw: `{"DiskImage":5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image, rootDisk := decodeInspectionRootfs(json.RawMessage(tt.raw))
			if image != tt.wantImage {
				t.Errorf("image = %q, want %q", image, tt.wantImage)
			}
			if (rootDisk != nil) != tt.wantRootDisk {
				t.Fatalf("rootDisk = %+v, want present=%v", rootDisk, tt.wantRootDisk)
			}
			if rootDisk != nil && rootDisk.SizeMiB != tt.wantSizeMiB {
				t.Errorf("rootDisk.SizeMiB = %d, want %d", rootDisk.SizeMiB, tt.wantSizeMiB)
			}
		})
	}
}

func TestModifyOptionsProjectsFields(t *testing.T) {
	want := msbSdk.ModifyOptions{
		CPUs:            4,
		MemoryMiB:       4096,
		RootDiskSizeMiB: 16384,
		Policy:          msbSdk.ModificationPolicyRestart,
	}
	got := modifyOptions(ModifyOptions{
		CPUs:            4,
		MemoryMiB:       4096,
		RootDiskSizeMiB: 16384,
		Policy:          ModificationPolicyRestart,
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modifyOptions() = %+v, want %+v", got, want)
	}
	if got := modifyOptions(ModifyOptions{}); !reflect.DeepEqual(got, msbSdk.ModifyOptions{}) {
		t.Errorf("modifyOptions(zero) = %+v, want the zero SDK options", got)
	}
}

func TestModificationPlanFromProjectsConflicts(t *testing.T) {
	plan := modificationPlanFrom(&msbSdk.SandboxModificationPlan{
		Sandbox: "sb",
		Applied: true,
		Conflicts: []msbSdk.ModificationConflict{
			{Field: "cpus", Message: "cannot shrink"},
		},
	})
	if plan == nil {
		t.Fatal("modificationPlanFrom() = nil, want a plan")
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].Field != "cpus" ||
		plan.Conflicts[0].Message != "cannot shrink" {
		t.Errorf("Conflicts = %+v, want the projected conflict", plan.Conflicts)
	}
}

func TestModificationPlanFromNilIsNil(t *testing.T) {
	if got := modificationPlanFrom(nil); got != nil {
		t.Errorf("modificationPlanFrom(nil) = %v, want nil", got)
	}
	if got := modificationPlanFrom(&msbSdk.SandboxModificationPlan{}); got.Conflicts != nil {
		t.Errorf("Conflicts = %v, want nil for a nil SDK conflict list", got.Conflicts)
	}
}

func TestDiskCompactionResultFromProjectsFields(t *testing.T) {
	result := diskCompactionResultFrom(&msbSdk.DiskCompactionResult{
		DryRun:            true,
		InputLayers:       4,
		SelectedLayers:    3,
		OutputLayers:      2,
		MaterializedBytes: 1024,
		TotalUs:           10,
		PauseUs:           5,
		Disks: []msbSdk.DiskCompactionDiskResult{{
			GuestPath:         "/data",
			InputLayers:       2,
			SelectedLayers:    1,
			OutputLayers:      1,
			MaterializedBytes: 512,
			TotalUs:           4,
		}},
	})
	if result == nil {
		t.Fatal("diskCompactionResultFrom() = nil, want a result")
	}
	if result.InputLayers != 4 || result.SelectedLayers != 3 || result.OutputLayers != 2 ||
		result.MaterializedBytes != 1024 || result.TotalUs != 10 || result.PauseUs != 5 {
		t.Errorf("diskCompactionResultFrom() = %+v, want every scalar field projected", result)
	}
	if len(result.Disks) != 1 || result.Disks[0].GuestPath != "/data" ||
		result.Disks[0].InputLayers != 2 || result.Disks[0].TotalUs != 4 {
		t.Errorf("Disks = %+v, want the projected disk result", result.Disks)
	}
}

func TestDiskCompactionResultFromNilIsNil(t *testing.T) {
	if got := diskCompactionResultFrom(nil); got != nil {
		t.Errorf("diskCompactionResultFrom(nil) = %v, want nil", got)
	}
}

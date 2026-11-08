package msb

// Reflection covers only the exported config structs. The SDK's jobExecConfig,
// jobListConfig, lifecycleOptions and destroyOptions are unexported and mutate
// unexported fields, so they cannot be reflected; drift there is caught only by
// the compiler when the adapters call the SDK constructors (Task 2.3).

import (
	"reflect"
	"sort"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestSDKConfigFieldsAreClassified(t *testing.T) {
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

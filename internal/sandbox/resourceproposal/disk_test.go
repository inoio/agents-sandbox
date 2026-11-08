package resourceproposal

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// diskTestLimits are a run's limits with the defaults: a 4 GiB root, a 16 GiB
// quota, a 2 GiB /tmp and 4 GiB of memory.
func diskTestLimits() DiskLimits {
	return DiskLimits{RootMiB: 4096, WorkspaceQuotaMiB: 16384, TmpMiB: 2048, MemoryMiB: 4096}
}

// probed is one mount's usage in a probe result.
type probed struct {
	kind  pressure.Kind
	usage pressure.DiskUsage
}

// probeResult builds a probe result from the given mounts.
func probeResult(mounts ...probed) map[pressure.Kind]pressure.DiskUsage {
	result := make(map[pressure.Kind]pressure.DiskUsage, len(mounts))
	for _, mount := range mounts {
		result[mount.kind] = mount.usage
	}
	return result
}

// fullTmp is a probe result with /tmp short of space and the rest fine.
func fullTmp() map[pressure.Kind]pressure.DiskUsage {
	return probeResult(
		probed{pressure.KindRootDisk, pressure.DiskUsage{SizeKiB: 4 << 20, UsedKiB: 1 << 20, AvailKiB: 3 << 20}},
		probed{
			pressure.KindWorkspaceQuota,
			pressure.DiskUsage{SizeKiB: 16 << 20, UsedKiB: 1 << 20, AvailKiB: 15 << 20},
		},
		probed{pressure.KindTmp, pressure.DiskUsage{SizeKiB: 2 << 20, UsedKiB: 1950 << 10, AvailKiB: 98 << 10}},
	)
}

// readDiskMarkers re-reads the key's disk markers.
func readDiskMarkers(t *testing.T, k state.Key) map[string]state.DiskPressureState {
	t.Helper()
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	return hs.DiskPressure
}

// resolveSetting reads key from the resolved launcher config for slug.
func resolveSetting(t *testing.T, slug string, read func(viperconfig.Config) string) string {
	t.Helper()
	r, err := viperconfig.NewResolver(nil, configpaths.Get(), slug)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return read(*r.Config())
}

// diskRun calls the disk core with the default limits.
func diskRun(
	t *testing.T,
	usages map[pressure.Kind]pressure.DiskUsage,
	ui termio.UI,
) map[string]string {
	t.Helper()
	k := detectKey()
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	return DetectAndOfferDiskRaise(
		k,
		usages,
		hs.DiskPressure,
		diskTestLimits(),
		options.RunOptions{},
		configpaths.Get(),
		ui,
	)
}

func unsetDiskEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DISK_SIZE", "WORKSPACE_QUOTA", "TMP_SIZE"} {
		testutil.UnsetEnv(t, "AGENTS_SANDBOX_"+key)
		testutil.UnsetEnv(t, "OPENCODE_SANDBOX_"+key)
	}
}

// TestDetectAndOfferDiskRaiseOffersAndPersists pins the primary path: a full
// /tmp is recorded, offered with its free space and a rebuild notice, and an
// accepted raise (the +50 % suggestion) is persisted and answered.
func TestDetectAndOfferDiskRaiseOffersAndPersists(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	var question, suggested string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			question = prompt
			return "raise", nil
		},
		InputFn: func(_, defaultValue string) (string, error) {
			suggested = defaultValue
			return defaultValue, nil
		},
	}

	raised := diskRun(t, fullTmp(), ui)

	if len(raised) != 1 || raised["tmp-size"] != "3072M" {
		t.Fatalf("raised = %v, want tmp-size 3072M", raised)
	}
	if suggested != "3G" {
		t.Errorf("suggested %q, want 3G (+50 %% of 2G)", suggested)
	}
	if !strings.Contains(question, "/tmp") || !strings.Contains(question, "98M free of 2G") ||
		!strings.Contains(question, "rebuild") {
		t.Errorf("question %q, want the mount, its free space and size, and the rebuild notice", question)
	}
	if len(ui.InfoCalls) != 1 || !strings.Contains(ui.InfoCalls[0], "tmp-size to 3G") {
		t.Errorf("info messages %q, want the saved tmp-size confirmed", ui.InfoCalls)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings %q, want none: /tmp draws on memory, not on a host filesystem", ui.WarnCalls)
	}
	if got := resolveSetting(t, k.Slug, func(c viperconfig.Config) string { return c.TmpSize }); got != "3072M" {
		t.Errorf("persisted tmp-size = %q, want 3072M", got)
	}
	marker := readDiskMarkers(t, k)["tmp"]
	if marker.SeenAt.IsZero() || marker.OfferedAt.IsZero() || marker.AvailKiB != 98<<10 {
		t.Errorf("tmp marker = %+v, want the hit recorded with its free space and answered", marker)
	}
}

// TestDetectAndOfferDiskRaiseRootAppliesLive pins the root disk's wording: its
// raise applies without a rebuild.
func TestDetectAndOfferDiskRaiseRootAppliesLive(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	var question string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			question = prompt
			return "keep", nil
		},
	}
	usages := probeResult(probed{pressure.KindRootDisk, pressure.DiskUsage{SizeKiB: 4 << 20, AvailKiB: 100 << 10}})

	if raised := diskRun(t, usages, ui); len(raised) != 0 {
		t.Errorf("raised = %v after a decline, want none", raised)
	}
	if !strings.Contains(question, "root disk") || !strings.Contains(question, "without a rebuild") {
		t.Errorf("question %q, want the root disk and a live apply", question)
	}
	if readDiskMarkers(t, k)["root-disk"].OfferedAt.IsZero() {
		t.Error("a decline left the root-disk hit unanswered")
	}
}

// TestDetectAndOfferDiskRaiseNonInteractiveThenNextLaunch pins the deferred
// offer: a non-interactive run records the hit without answering it, and the
// next launch (no probe) offers it with the recorded free space.
func TestDetectAndOfferDiskRaiseNonInteractiveThenNextLaunch(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})

	quiet := &termio.Mock{}
	if raised := diskRun(t, fullTmp(), quiet); len(raised) != 0 {
		t.Fatalf("non-interactive raised = %v, want none", raised)
	}
	if !verboseContains(quiet, "later interactive run") {
		t.Errorf("verbose calls %v, want the deferred offer noted", quiet.VerboseCalls)
	}
	if marker := readDiskMarkers(t, k)["tmp"]; marker.SeenAt.IsZero() || !marker.OfferedAt.IsZero() {
		t.Fatalf("tmp marker = %+v, want recorded and unanswered", marker)
	}

	var question string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			question = prompt
			return "raise", nil
		},
		InputFn: func(_, defaultValue string) (string, error) { return defaultValue, nil },
	}
	raised := diskRun(t, nil, ui)

	if raised["tmp-size"] != "3072M" || !strings.Contains(question, "98M free") {
		t.Errorf(
			"next launch raised %v with %q, want tmp-size 3072M offered with the recorded free space",
			raised,
			question,
		)
	}
}

// TestDetectAndOfferDiskRaiseAnsweredHitStaysClosed pins the hysteresis: an
// answered hit is not re-offered while its mount stays short, and a recovered
// mount clears the marker so a later trip is offered again.
func TestDetectAndOfferDiskRaiseAnsweredHitStaysClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seen := time.Now().Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{DiskPressure: map[string]state.DiskPressureState{
		"tmp": {SeenAt: seen, SizeKiB: 2 << 20, AvailKiB: 98 << 10, OfferedAt: seen},
	}})
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	diskRun(t, fullTmp(), ui)
	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 for an answered hit", selects)
	}

	recovered := fullTmp()
	recovered[pressure.KindTmp] = pressure.DiskUsage{SizeKiB: 2 << 20, AvailKiB: 1 << 20}
	diskRun(t, recovered, ui)
	if _, ok := readDiskMarkers(t, k)["tmp"]; ok {
		t.Fatal("a recovered /tmp kept its marker")
	}

	diskRun(t, fullTmp(), ui)
	if selects != 1 {
		t.Errorf("Select called %d times, want the new trip offered", selects)
	}
}

// TestDetectAndOfferDiskRaiseOffersEachTrippedLimit pins that one probe can
// trip several limits and each is offered and persisted on its own.
func TestDetectAndOfferDiskRaiseOffersEachTrippedLimit(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn:             func(_, defaultValue string) (string, error) { return defaultValue, nil },
	}
	usages := probeResult(
		probed{pressure.KindRootDisk, pressure.DiskUsage{SizeKiB: 4 << 20, AvailKiB: 100 << 10}},
		probed{pressure.KindWorkspaceQuota, pressure.DiskUsage{SizeKiB: 16 << 20, AvailKiB: 100 << 10}},
	)

	raised := diskRun(t, usages, ui)

	if len(raised) != 2 || raised["disk-size"] != "6144M" || raised["workspace-quota"] != "24576M" {
		t.Errorf("raised = %v, want disk-size 6144M and workspace-quota 24576M", raised)
	}
}

// TestDetectAndOfferDiskRaiseExplainsWhatItCannotOffer pins the cases with no
// prompt: a size set by its flag or environment variable, and a /tmp already
// as large as the VM's memory. Each is explained and counts as answered.
func TestDetectAndOfferDiskRaiseExplainsWhatItCannotOffer(t *testing.T) {
	tests := []struct {
		name     string
		opts     options.RunOptions
		env      string
		limits   DiskLimits
		wantWarn string
	}{
		{
			name:     "flag",
			opts:     options.RunOptions{ExplicitFlags: []string{"tmp-size"}},
			limits:   diskTestLimits(),
			wantWarn: "--tmp-size",
		},
		{
			name:     "environment",
			env:      "AGENTS_SANDBOX_TMP_SIZE",
			limits:   diskTestLimits(),
			wantWarn: "AGENTS_SANDBOX_TMP_SIZE",
		},
		{
			name:     "tmp at the memory limit",
			limits:   DiskLimits{RootMiB: 4096, WorkspaceQuotaMiB: 16384, TmpMiB: 2048, MemoryMiB: 2048},
			wantWarn: "memory limit",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			unsetDiskEnv(t)
			if tc.env != "" {
				t.Setenv(tc.env, "2G")
			}
			k := detectKey()
			seedDetectState(t, k, state.HomeState{})
			prompted := false
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn: func(string, []termio.Choice, string) (string, error) {
					prompted = true
					return "raise", nil
				},
			}

			raised := DetectAndOfferDiskRaise(k, fullTmp(), nil, tc.limits, tc.opts, configpaths.Get(), ui)

			if prompted || len(raised) != 0 {
				t.Errorf("prompted=%v raised=%v, want neither", prompted, raised)
			}
			if !warnContains(ui, tc.wantWarn) {
				t.Errorf("warnings %q, want one naming %q", ui.WarnCalls, tc.wantWarn)
			}
			if readDiskMarkers(t, k)["tmp"].OfferedAt.IsZero() {
				t.Error("the explained hit stayed unanswered")
			}
		})
	}
}

// TestDetectAndOfferDiskRaiseCapsTmpBelowMemory pins the /tmp bound: the
// suggestion and an accepted value stay strictly below the VM's memory limit,
// since an explicit tmp-size equal to memory is rejected by options.ValidateSizes
// on the next launch.
func TestDetectAndOfferDiskRaiseCapsTmpBelowMemory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	var suggested string
	var prompts []string
	answers := []string{"8192M", "6144M"}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(prompt, defaultValue string) (string, error) {
			suggested = defaultValue
			prompts = append(prompts, prompt)
			answer := answers[0]
			answers = answers[1:]
			return answer, nil
		},
	}
	limits := DiskLimits{RootMiB: 4096, WorkspaceQuotaMiB: 16384, TmpMiB: 4096, MemoryMiB: 8192}

	raised := DetectAndOfferDiskRaise(k, fullTmp(), nil, limits, options.RunOptions{}, configpaths.Get(), ui)

	if suggested != "6G" || raised["tmp-size"] != "6144M" {
		t.Errorf("suggested %q, raised %v; want 6144M, the largest whole GiB below the 8192M memory", suggested, raised)
	}
	if len(prompts) != 2 || !strings.Contains(prompts[1], "VM memory limit") {
		t.Errorf("prompts %q, want the memory-equal 8192M rejected with the memory limit as reason", prompts)
	}
	if err := options.ValidateSizes("8G", raised["tmp-size"], "", ""); err != nil {
		t.Errorf("the accepted raise does not survive ValidateSizes: %v", err)
	}
}

// TestTmpRaiseMaximumMiB pins the tmp raise maximum: the largest whole GiB
// strictly below the memory, falling back to memory-1 when no whole GiB fits.
func TestTmpRaiseMaximumMiB(t *testing.T) {
	tests := []struct{ memoryMiB, want uint32 }{
		{0, 0},
		{128, 127},
		{1024, 1023},
		{1536, 1024},
		{2048, 1024},
		{2049, 2048},
		{2560, 2048},
		{4096, 3072},
		{8192, 7168},
	}
	for _, tc := range tests {
		if got := tmpRaiseMaximumMiB(tc.memoryMiB); got != tc.want {
			t.Errorf("tmpRaiseMaximumMiB(%d) = %d, want %d", tc.memoryMiB, got, tc.want)
		}
	}
}

// TestDetectAndOfferDiskRaiseFailuresAreBestEffort pins that a failing prompt
// leaves the hit unanswered and a failing write warns, and neither raises.
func TestDetectAndOfferDiskRaiseFailuresAreBestEffort(t *testing.T) {
	t.Run("prompt error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		unsetDiskEnv(t)
		k := detectKey()
		seedDetectState(t, k, state.HomeState{})
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { return "", errors.New("stdin closed") },
		}
		if raised := diskRun(t, fullTmp(), ui); len(raised) != 0 {
			t.Errorf("raised = %v, want none", raised)
		}
		if !readDiskMarkers(t, k)["tmp"].OfferedAt.IsZero() {
			t.Error("a failed prompt answered the hit")
		}
		if !verboseContains(ui, "disk raise prompt failed") {
			t.Errorf("verbose calls %v do not report the prompt failure", ui.VerboseCalls)
		}
	})
	t.Run("write error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		unsetDiskEnv(t)
		k := detectKey()
		seedDetectState(t, k, state.HomeState{})
		testutil.WriteFile(t, configpaths.Get().UserSlugConfigDir(k.Slug), "config.json", "{}")
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
			InputFn:             func(_, defaultValue string) (string, error) { return defaultValue, nil },
		}
		if raised := diskRun(t, fullTmp(), ui); len(raised) != 0 {
			t.Errorf("raised = %v, want none after a failed write", raised)
		}
		if !warnContains(ui, "could not persist") {
			t.Errorf("warnings %q, want the write failure", ui.WarnCalls)
		}
	})
}

// TestDetectAndOfferDiskRaiseStateWriteFailuresAreBestEffort pins that failing
// marker writes (record, answer, clear) are logged at verbose and never stop
// the offer.
func TestDetectAndOfferDiskRaiseStateWriteFailuresAreBestEffort(t *testing.T) {
	withFailingDetectStateDir(t)
	unsetDiskEnv(t)
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "keep", nil },
	}
	usages := fullTmp()
	usages[pressure.KindRootDisk] = pressure.DiskUsage{SizeKiB: 4 << 20, AvailKiB: 3 << 20}
	markers := map[string]state.DiskPressureState{"root-disk": {SeenAt: time.Now()}}

	DetectAndOfferDiskRaise(detectKey(), usages, markers, diskTestLimits(), options.RunOptions{}, configpaths.Get(), ui)

	for _, want := range []string{
		"recording the full tmp failed",
		"recording the tmp raise answer failed",
		"clearing the recovered root-disk failed",
	} {
		if !verboseContains(ui, want) {
			t.Errorf("verbose calls %v, want %q", ui.VerboseCalls, want)
		}
	}
}

// TestRunDiskLimits pins the limits a run's VM is set up with: the configured
// sizes or their defaults, and the memory limit /tmp shares.
func TestRunDiskLimits(t *testing.T) {
	got := RunDiskLimits(options.RunOptions{DiskSize: "8G", TmpSize: "", WorkspaceQuota: "32G", Memory: "6G"})
	want := DiskLimits{RootMiB: 8192, WorkspaceQuotaMiB: 32768, TmpMiB: options.DefaultTmpSizeCapMiB, MemoryMiB: 6144}
	if got != want {
		t.Errorf("RunDiskLimits = %+v, want %+v", got, want)
	}
}

// TestRunDiskLimitsDerivesTmpFromMemory pins that the offer's current /tmp is
// the memory-relative default when tmp-size is unset.
func TestRunDiskLimitsDerivesTmpFromMemory(t *testing.T) {
	got := RunDiskLimits(options.RunOptions{Memory: "2G"})
	if got.TmpMiB != 1024 {
		t.Errorf("TmpMiB = %d, want 1024 (memory/2)", got.TmpMiB)
	}
}

// TestDetectAndOfferDiskRaiseWarnsWhenTheHostLacksTheSpace pins the host-bound
// warning: an accepted root-disk raise larger than the free space on the host
// filesystem holding the runtime home is still saved, with a warning; an
// unknown free figure warns nothing.
func TestDetectAndOfferDiskRaiseWarnsWhenTheHostLacksTheSpace(t *testing.T) {
	for _, tc := range []struct {
		name        string
		freeMiB     uint32
		known       bool
		wantWarning bool
	}{
		{name: "raise above the free space", freeMiB: 1024, known: true, wantWarning: true},
		{name: "raise equal to the free space", freeMiB: 2048, known: true},
		{name: "free space unknown", known: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			unsetDiskEnv(t)
			k := detectKey()
			seedDetectState(t, k, state.HomeState{})
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
				InputFn:             func(_, defaultValue string) (string, error) { return defaultValue, nil },
			}
			limits := diskTestLimits()
			limits.RootHostFreeMiB, limits.RootHostFreeKnown = tc.freeMiB, tc.known
			usages := probeResult(
				probed{pressure.KindRootDisk, pressure.DiskUsage{SizeKiB: 4 << 20, AvailKiB: 100 << 10}},
			)

			raised := DetectAndOfferDiskRaise(k, usages, nil, limits, options.RunOptions{}, configpaths.Get(), ui)

			if raised["disk-size"] != "6144M" {
				t.Errorf("raised = %v, want disk-size 6144M saved regardless of the warning", raised)
			}
			if warned := warnContains(ui, "the host filesystem holding the sandbox runtime"); warned != tc.wantWarning {
				t.Errorf("warnings %q, want a host-space warning naming the runtime's filesystem = %v",
					ui.WarnCalls, tc.wantWarning)
			}
		})
	}
}

// TestDetectAndOfferDiskRaiseWarnsForTheWorkspaceQuota pins that the quota's
// raise is checked against the repository's filesystem, only when its free
// space is known and smaller than the increase.
func TestDetectAndOfferDiskRaiseWarnsForTheWorkspaceQuota(t *testing.T) {
	for _, tc := range []struct {
		name        string
		freeMiB     uint32
		known       bool
		wantWarning bool
	}{
		{name: "raise above the free space", freeMiB: 1024, known: true, wantWarning: true},
		{name: "plenty of free space", freeMiB: 1 << 20, known: true},
		{name: "free space unknown", known: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			unsetDiskEnv(t)
			k := detectKey()
			seedDetectState(t, k, state.HomeState{})
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
				InputFn:             func(_, defaultValue string) (string, error) { return defaultValue, nil },
			}
			limits := diskTestLimits()
			limits.WorkspaceHostFreeMiB, limits.WorkspaceHostFreeKnown = tc.freeMiB, tc.known
			usages := probeResult(
				probed{pressure.KindWorkspaceQuota, pressure.DiskUsage{SizeKiB: 16 << 20, AvailKiB: 100 << 10}},
			)

			DetectAndOfferDiskRaise(k, usages, nil, limits, options.RunOptions{}, configpaths.Get(), ui)

			if warned := warnContains(ui, "repository"); warned != tc.wantWarning {
				t.Errorf("warnings %q, want a repository-space warning = %v", ui.WarnCalls, tc.wantWarning)
			}
		})
	}
}

// TestWithHostFreeSpace pins that the host free-space figures are filled in
// for the root disk and the workspace quota from existing paths.
func TestWithHostFreeSpace(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	limits := WithHostFreeSpace(diskTestLimits())
	if !limits.RootHostFreeKnown || !limits.WorkspaceHostFreeKnown {
		t.Errorf("WithHostFreeSpace = %+v, want both host figures known", limits)
	}
	if limits.RootMiB != 4096 || limits.TmpMiB != 2048 {
		t.Errorf("WithHostFreeSpace = %+v, want the sizes kept", limits)
	}
}

// TestDetectAndOfferDiskRaiseStartsFromTheMeasuredSize pins that a VM larger
// than its configured size (created with a flag, launched without it) is raised
// from its measured size, so the offer never suggests a smaller disk, which the
// next launch would treat as a decrease and rebuild.
func TestDetectAndOfferDiskRaiseStartsFromTheMeasuredSize(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	var proposed string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(_, defaultValue string) (string, error) {
			proposed = defaultValue
			return defaultValue, nil
		},
	}
	usages := probeResult(probed{pressure.KindRootDisk, pressure.DiskUsage{SizeKiB: 16 << 20, AvailKiB: 100 << 10}})

	raised := DetectAndOfferDiskRaise(k, usages, nil, diskTestLimits(), options.RunOptions{}, configpaths.Get(), ui)

	if proposed != "24G" || raised["disk-size"] != "24576M" {
		t.Errorf("proposed %q, raised %v; want 24G from the measured 16 GiB, not from the configured 4 GiB",
			proposed, raised)
	}
}

// TestDetectAndOfferDiskRaiseUsesTheStoredHit pins the race guard against a
// stale marker read: a hit another session recorded and answered after the
// caller read the markers is not offered again, and a recovery the caller saw
// for an older hit does not clear a newer one.
func TestDetectAndOfferDiskRaiseUsesTheStoredHit(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seen := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	answered := state.DiskPressureState{SeenAt: seen, SizeKiB: 2 << 20, AvailKiB: 10 << 10, OfferedAt: seen}
	newer := state.DiskPressureState{SeenAt: seen.Add(time.Hour), SizeKiB: 16 << 20, AvailKiB: 10 << 10}
	seedDetectState(t, k, state.HomeState{DiskPressure: map[string]state.DiskPressureState{
		string(pressure.KindTmp):            answered,
		string(pressure.KindWorkspaceQuota): newer,
	}})
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			selects++
			return "raise", nil
		},
		InputFn: func(_, defaultValue string) (string, error) { return defaultValue, nil },
	}
	usages := probeResult(
		probed{pressure.KindTmp, pressure.DiskUsage{SizeKiB: 2 << 20, AvailKiB: 10 << 10}},
		probed{pressure.KindWorkspaceQuota, pressure.DiskUsage{SizeKiB: 16 << 20, AvailKiB: 15 << 20}},
	)
	staleMarkers := map[string]state.DiskPressureState{
		string(pressure.KindWorkspaceQuota): {SeenAt: seen, SizeKiB: 16 << 20},
	}

	DetectAndOfferDiskRaise(k, usages, staleMarkers, diskTestLimits(), options.RunOptions{}, configpaths.Get(), ui)

	if selects != 0 {
		t.Errorf("prompted %d times, want 0: the stored /tmp hit was already answered", selects)
	}
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got := hs.DiskPressure[string(pressure.KindWorkspaceQuota)]; !got.SeenAt.Equal(newer.SeenAt) {
		t.Errorf("workspace-quota marker = %+v, want the newer hit kept", got)
	}
}

// TestDetectAndOfferDiskRaiseJudgesTheWorkspaceByItsQuota pins that the
// /workspace mount's size, which counts the host repository's content on top of
// the quota, is not taken for the quota: a large repository with a small quota
// trips only when the quota runs short, and the raise starts from the quota.
func TestDetectAndOfferDiskRaiseJudgesTheWorkspaceByItsQuota(t *testing.T) {
	for _, tc := range []struct {
		name         string
		quotaMiB     uint32
		usage        pressure.DiskUsage
		wantProposed string
	}{
		{
			name:     "a large repository with quota to spare",
			quotaMiB: 1024,
			usage:    pressure.DiskUsage{SizeKiB: 51 << 20, AvailKiB: 900 << 10},
		},
		{
			name:         "a short quota under a large repository",
			quotaMiB:     16384,
			usage:        pressure.DiskUsage{SizeKiB: 26 << 20, AvailKiB: 100 << 10},
			wantProposed: "24G",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			unsetDiskEnv(t)
			k := detectKey()
			seedDetectState(t, k, state.HomeState{})
			var proposed string
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
				InputFn: func(_, defaultValue string) (string, error) {
					proposed = defaultValue
					return defaultValue, nil
				},
			}
			limits := diskTestLimits()
			limits.WorkspaceQuotaMiB = tc.quotaMiB

			DetectAndOfferDiskRaise(k, probeResult(probed{pressure.KindWorkspaceQuota, tc.usage}), nil, limits,
				options.RunOptions{}, configpaths.Get(), ui)

			if proposed != tc.wantProposed {
				t.Errorf("proposed %q, want %q", proposed, tc.wantProposed)
			}
		})
	}
}

// TestDetectAndOfferDiskRaiseStartsFromTheLargerSize pins that the configured
// limit and the mount the probe measured are both considered, and the offer
// starts from the larger: a configured limit above the live mount (a size set
// for the next launch but not yet applied) is the starting point, never the
// smaller measured mount.
func TestDetectAndOfferDiskRaiseStartsFromTheLargerSize(t *testing.T) {
	for _, tc := range []struct {
		name         string
		kind         pressure.Kind
		limits       DiskLimits
		usage        pressure.DiskUsage
		wantProposed string
	}{
		{
			name:         "root disk configured above the live size",
			kind:         pressure.KindRootDisk,
			limits:       DiskLimits{RootMiB: 16384, WorkspaceQuotaMiB: 16384, TmpMiB: 2048, MemoryMiB: 4096},
			usage:        pressure.DiskUsage{SizeKiB: 4 << 20, AvailKiB: 100 << 10},
			wantProposed: "24G",
		},
		{
			name:         "/tmp configured above the live size",
			kind:         pressure.KindTmp,
			limits:       DiskLimits{RootMiB: 4096, WorkspaceQuotaMiB: 16384, TmpMiB: 16384, MemoryMiB: 65536},
			usage:        pressure.DiskUsage{SizeKiB: 2 << 20, AvailKiB: 98 << 10},
			wantProposed: "24G",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			unsetDiskEnv(t)
			k := detectKey()
			seedDetectState(t, k, state.HomeState{})
			var proposed string
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
				InputFn: func(_, defaultValue string) (string, error) {
					proposed = defaultValue
					return defaultValue, nil
				},
			}

			DetectAndOfferDiskRaise(
				k, probeResult(probed{tc.kind, tc.usage}), nil, tc.limits, options.RunOptions{}, configpaths.Get(), ui,
			)

			if proposed != tc.wantProposed {
				t.Errorf("proposed %q, want %q from the configured size", proposed, tc.wantProposed)
			}
		})
	}
}

// TestDetectAndOfferDiskRaiseRefreshesAStaleMarker pins the race guard for a
// marker the caller read before another session answered it: even though the
// caller passes the older, unanswered copy, the offer uses the marker stored
// now, so the answered hit is not offered again.
func TestDetectAndOfferDiskRaiseRefreshesAStaleMarker(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetDiskEnv(t)
	k := detectKey()
	seen := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	answered := state.DiskPressureState{SeenAt: seen, SizeKiB: 2 << 20, AvailKiB: 10 << 10, OfferedAt: seen}
	seedDetectState(t, k, state.HomeState{DiskPressure: map[string]state.DiskPressureState{
		string(pressure.KindTmp): answered,
	}})
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}
	// The caller read the marker before it was answered: it carries the older,
	// unanswered copy while the stored marker is answered.
	staleMarkers := map[string]state.DiskPressureState{
		string(pressure.KindTmp): {SeenAt: seen, SizeKiB: 2 << 20, AvailKiB: 10 << 10},
	}
	usages := probeResult(probed{pressure.KindTmp, pressure.DiskUsage{SizeKiB: 2 << 20, AvailKiB: 98 << 10}})

	DetectAndOfferDiskRaise(k, usages, staleMarkers, diskTestLimits(), options.RunOptions{}, configpaths.Get(), ui)

	if selects != 0 {
		t.Errorf("prompted %d times, want 0: the offer must use the marker stored now (already answered)", selects)
	}
}

// TestSuggestDiskRaise pins the disk suggestion: +50 % rounded up to a whole
// GiB, at least +1 GiB, capped at a known maximum and never below the current.
func TestSuggestDiskRaise(t *testing.T) {
	tests := []struct {
		currentMiB, maxMiB, want uint32
	}{
		{currentMiB: 2048, maxMiB: 0, want: 3072},
		{currentMiB: 1024, maxMiB: 0, want: 2048},
		{currentMiB: 500, maxMiB: 0, want: 1524},
		{currentMiB: 16384, maxMiB: 0, want: 24576},
		{currentMiB: 2048, maxMiB: 2560, want: 2560},
		{currentMiB: 2048, maxMiB: 1024, want: 2048},
	}
	for _, tc := range tests {
		if got := SuggestDiskRaise(tc.currentMiB, tc.maxMiB); got != tc.want {
			t.Errorf("SuggestDiskRaise(%d, %d) = %d, want %d", tc.currentMiB, tc.maxMiB, got, tc.want)
		}
	}
}

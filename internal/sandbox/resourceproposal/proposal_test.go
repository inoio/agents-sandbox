package resourceproposal

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

const testSlug = "widget-bbb"

// TestSuggestRaiseRoundsUpToNextGiB pins the suggested increment: 25% over the
// current value, rounded up to the next whole GiB.
func TestSuggestRaiseRoundsUpToNextGiB(t *testing.T) {
	if got := SuggestRaise(4096, 0); got != 5120 {
		t.Errorf("SuggestRaise(4096, 0) = %d; want 5120", got)
	}
}

// TestSuggestRaiseRoundsUpFromAFractionalGiB pins the rounding direction.
func TestSuggestRaiseRoundsUpFromAFractionalGiB(t *testing.T) {
	if got := SuggestRaise(5000, 0); got != 7168 {
		t.Errorf("SuggestRaise(5000, 0) = %d; want 7168 (6250 rounded up to the next GiB)", got)
	}
}

// TestSuggestRaiseAtLeastOneGiBMore pins the floor: even when 25% and rounding
// up land below current+1GiB, the raise is at least one GiB.
func TestSuggestRaiseAtLeastOneGiBMore(t *testing.T) {
	if got := SuggestRaise(800, 0); got != 1824 {
		t.Errorf("SuggestRaise(800, 0) = %d; want 1824 (current + 1 GiB floor)", got)
	}
}

// TestSuggestRaiseClampsToBootMax pins the upper clamp against the VM's boot
// maximum.
func TestSuggestRaiseClampsToBootMax(t *testing.T) {
	if got := SuggestRaise(4096, 4096); got != 4096 {
		t.Errorf("SuggestRaise(4096, 4096) = %d; want 4096", got)
	}
}

// TestSuggestRaiseNeverBelowCurrent pins that a boot maximum below the current
// value cannot lower the suggestion.
func TestSuggestRaiseNeverBelowCurrent(t *testing.T) {
	if got := SuggestRaise(4096, 1000); got != 4096 {
		t.Errorf("SuggestRaise(4096, 1000) = %d; want 4096 (never below current)", got)
	}
}

// TestSuggestRaiseUnknownBootMaxNoClamp pins that a zero boot maximum means
// "unknown" and does not clamp.
func TestSuggestRaiseUnknownBootMaxNoClamp(t *testing.T) {
	if got := SuggestRaise(4096, 0); got == 4096 {
		t.Errorf("SuggestRaise(4096, 0) = %d; want a raise above current when the max is unknown", got)
	}
}

// TestAskNonInteractiveDeclinesWithoutPrompting pins the never-block contract:
// a non-interactive UI returns ok=false and neither Select nor Input is called.
func TestAskNonInteractiveDeclinesWithoutPrompting(t *testing.T) {
	var selectCalled, inputCalled bool
	ui := &termio.Mock{
		IsInteractiveResult: false,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			selectCalled = true
			return "raise", nil
		},
		InputFn: func(string, string) (string, error) {
			inputCalled = true
			return "6G", nil
		},
	}
	hit := pressure.Signal{Kind: pressure.KindMemory, Evidence: pressure.EvidenceOOMLog, Victim: "opencode"}

	got, ok, err := Ask(ui, hit, Suggestion{CurrentMiB: 4096, ProposedMiB: 5120})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ok {
		t.Errorf("ok = true; want false for a non-interactive UI")
	}
	if got != 0 {
		t.Errorf("newMiB = %d; want 0", got)
	}
	if selectCalled || inputCalled {
		t.Errorf("Select called=%v Input called=%v; want neither", selectCalled, inputCalled)
	}
}

// TestAskInteractiveAcceptsInput pins the accept path: Select "raise" then Input
// the new size, parsed to MiB.
func TestAskInteractiveAcceptsInput(t *testing.T) {
	var selectPrompt string
	var selectKeys []string
	var selectDefault string
	var inputPrompt, inputDefault string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, choices []termio.Choice, defaultKey string) (string, error) {
			selectPrompt = prompt
			selectDefault = defaultKey
			for _, c := range choices {
				selectKeys = append(selectKeys, c.Key)
			}
			return "raise", nil
		},
		InputFn: func(prompt, defaultValue string) (string, error) {
			inputPrompt = prompt
			inputDefault = defaultValue
			return "6G", nil
		},
	}
	hit := pressure.Signal{Kind: pressure.KindMemory, Evidence: pressure.EvidenceOOMLog, Victim: "opencode"}

	got, ok, err := Ask(ui, hit, Suggestion{CurrentMiB: 4096, ProposedMiB: 5120})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !ok {
		t.Fatal("ok = false; want true")
	}
	if got != 6144 {
		t.Errorf("newMiB = %d; want 6144", got)
	}
	if selectDefault != "raise" {
		t.Errorf("Select default = %q; want raise", selectDefault)
	}
	if len(selectKeys) != 2 || selectKeys[0] != "raise" || selectKeys[1] != "keep" {
		t.Errorf("Select keys = %v; want [raise keep]", selectKeys)
	}
	if inputDefault != "5G" {
		t.Errorf("Input default = %q; want 5G for a whole-GiB suggestion", inputDefault)
	}
	if !strings.Contains(inputPrompt, "memory") {
		t.Errorf("Input prompt = %q; want it to mention memory", inputPrompt)
	}
	if !strings.Contains(selectPrompt, "opencode") {
		t.Errorf("Select prompt = %q; want it to name the OOM victim", selectPrompt)
	}
}

// TestAskDeclineReturnsNotOk pins that a "keep" answer is a decline: ok=false
// and no value prompt.
func TestAskDeclineReturnsNotOk(t *testing.T) {
	var inputCalled bool
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			return "keep", nil
		},
		InputFn: func(string, string) (string, error) {
			inputCalled = true
			return "6G", nil
		},
	}
	got, ok, err := Ask(
		ui,
		pressure.Signal{Kind: pressure.KindMemory, Evidence: pressure.EvidenceHeadroom},
		Suggestion{CurrentMiB: 4096, ProposedMiB: 5120},
	)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ok || got != 0 {
		t.Errorf("Ask = (%d, %v); want (0, false) on decline", got, ok)
	}
	if inputCalled {
		t.Error("Input was called after a decline")
	}
}

// TestAskRepromptsUntilAValidRaise pins the input validation: an answer that is
// not a size, does not raise the current limit, or exceeds the boot maximum is
// rejected with the reason in the next prompt, and the first valid answer wins.
func TestAskRepromptsUntilAValidRaise(t *testing.T) {
	tests := []struct {
		name       string
		rejected   string
		wantReason string
	}{
		{name: "not a size", rejected: "8GB", wantReason: `"8GB" is not a size`},
		{name: "zero", rejected: "0", wantReason: "must be above the current 8G"},
		{name: "below the current limit", rejected: "2G", wantReason: "must be above the current 8G"},
		{name: "equal to the current limit", rejected: "8G", wantReason: "must be above the current 8G"},
		{name: "above the boot maximum", rejected: "32G", wantReason: "must not exceed the host maximum 16G"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var prompts []string
			answers := []string{tc.rejected, "12G"}
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn: func(string, []termio.Choice, string) (string, error) {
					return "raise", nil
				},
				InputFn: func(prompt, _ string) (string, error) {
					prompts = append(prompts, prompt)
					answer := answers[0]
					answers = answers[1:]
					return answer, nil
				},
			}
			got, ok, err := Ask(
				ui,
				pressure.Signal{Kind: pressure.KindMemory, Evidence: pressure.EvidenceHeadroom},
				Suggestion{CurrentMiB: 8192, ProposedMiB: 10240, BootMaxMiB: 16384},
			)
			if err != nil || !ok || got != 12288 {
				t.Fatalf("Ask = %d, %v, %v; want 12288, true, nil", got, ok, err)
			}
			if len(prompts) != 2 || !strings.Contains(prompts[1], tc.wantReason) {
				t.Errorf("prompts = %q; want a second prompt naming %q", prompts, tc.wantReason)
			}
		})
	}
}

// TestRaiseProblemHonoursEveryKnownBootMaximum pins that any known boot
// maximum, however small, bounds the raise; only zero means unknown.
func TestRaiseProblemHonoursEveryKnownBootMaximum(t *testing.T) {
	if problem := raiseProblem("2M", 2, true, Suggestion{BootMaxMiB: 1}); problem == "" {
		t.Error("a raise above a 1 MiB boot maximum was accepted")
	}
	if problem := raiseProblem("2M", 2, true, Suggestion{BootMaxMiB: 0}); problem != "" {
		t.Errorf("raise under an unknown boot maximum rejected: %q", problem)
	}
}

// TestAskRepromptInputErrorIsReturned pins that a failing read while
// re-prompting (stdin closed) ends the offer with the error instead of looping.
func TestAskRepromptInputErrorIsReturned(t *testing.T) {
	calls := 0
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			return "raise", nil
		},
		InputFn: func(string, string) (string, error) {
			calls++
			if calls == 1 {
				return "banana", nil
			}
			return "", io.EOF
		},
	}
	_, ok, err := Ask(
		ui,
		pressure.Signal{Kind: pressure.KindMemory, Evidence: pressure.EvidenceHeadroom},
		Suggestion{CurrentMiB: 8192, ProposedMiB: 10240},
	)
	if !errors.Is(err, io.EOF) || ok {
		t.Fatalf("Ask ok=%v err=%v; want the read error", ok, err)
	}
}

// TestAskInputDefaultUsesMiBForNonGiBValue pins the default formatting: a
// suggestion that is not a whole GiB is shown in MiB.
func TestAskInputDefaultUsesMiBForNonGiBValue(t *testing.T) {
	var inputDefault string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			return "raise", nil
		},
		InputFn: func(_, defaultValue string) (string, error) {
			inputDefault = defaultValue
			return defaultValue, nil
		},
	}
	if _, _, err := Ask(
		ui,
		pressure.Signal{Kind: pressure.KindMemory, Evidence: pressure.EvidenceHeadroom},
		Suggestion{CurrentMiB: 4096, ProposedMiB: 5000},
	); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if inputDefault != "5000M" {
		t.Errorf("Input default = %q; want 5000M", inputDefault)
	}
}

// TestAskSelectErrorPropagates pins that a prompt failure is surfaced.
func TestAskSelectErrorPropagates(t *testing.T) {
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			return "", errBoom
		},
	}
	got, ok, err := Ask(ui, pressure.Signal{}, Suggestion{})
	if err == nil {
		t.Fatal("expected the Select error to propagate")
	}
	if ok || got != 0 {
		t.Errorf("Ask = (%d, %v), want (0, false) on a Select error", got, ok)
	}
}

// TestAskInputErrorPropagates pins that an input failure is surfaced.
func TestAskInputErrorPropagates(t *testing.T) {
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			return "raise", nil
		},
		InputFn: func(string, string) (string, error) {
			return "", errBoom
		},
	}
	got, ok, err := Ask(ui, pressure.Signal{}, Suggestion{})
	if err == nil {
		t.Fatal("expected the Input error to propagate")
	}
	if ok || got != 0 {
		t.Errorf("Ask = (%d, %v), want (0, false) on an Input error", got, ok)
	}
}

// TestSuggestRaiseKeepsProposalBelowBootMax pins that a suggestion below the
// boot maximum is left alone rather than clamped up to it.
func TestSuggestRaiseKeepsProposalBelowBootMax(t *testing.T) {
	if got := SuggestRaise(4096, 8192); got != 5120 {
		t.Errorf("SuggestRaise(4096, 8192) = %d; want 5120 (below the boot max)", got)
	}
}

// TestSuggestRaiseBootMaxOfOneMiB pins the boot-max presence check: a maximum of
// exactly 1 MiB still clamps (and never lowers below the current value).
func TestSuggestRaiseBootMaxOfOneMiB(t *testing.T) {
	if got := SuggestRaise(800, 1); got != 800 {
		t.Errorf("SuggestRaise(800, 1) = %d; want 800 (never below current)", got)
	}
}

// TestRoundUpGiB pins the rounding, including the near-zero input where the
// off-by-one in the numerator matters.
func TestRoundUpGiB(t *testing.T) {
	cases := map[uint32]uint32{0: 0, 1: 1024, 1024: 1024, 1025: 2048, 4096: 4096}
	for in, want := range cases {
		if got := roundUpGiB(in); got != want {
			t.Errorf("roundUpGiB(%d) = %d; want %d", in, got, want)
		}
	}
}

// TestSelectPromptNamesVictimOnlyForOOM pins the prompt framing: only an
// OOM-log hit with a named victim uses the victim wording; a headroom hit or an
// OOM hit with no victim uses the generic low-memory wording.
func TestSelectPromptNamesVictimOnlyForOOM(t *testing.T) {
	victim := selectPrompt(pressure.Signal{Evidence: pressure.EvidenceOOMLog, Victim: "opencode"})
	if !strings.Contains(victim, "opencode") || !strings.Contains(victim, "ran out of memory") {
		t.Errorf("OOM prompt = %q; want it to name the victim", victim)
	}
	headroomWithVictim := selectPrompt(pressure.Signal{Evidence: pressure.EvidenceHeadroom, Victim: "opencode"})
	if strings.Contains(headroomWithVictim, "opencode") ||
		!strings.Contains(headroomWithVictim, "running low on memory") {
		t.Errorf("headroom prompt = %q; want the generic wording", headroomWithVictim)
	}
	oomNoVictim := selectPrompt(pressure.Signal{Evidence: pressure.EvidenceOOMLog})
	if strings.Contains(oomNoVictim, "ran out of memory") || !strings.Contains(oomNoVictim, "running low on memory") {
		t.Errorf("victimless OOM prompt = %q; want the generic wording", oomNoVictim)
	}
}

// TestPersistMemoryRejectsEnvShadow pins that either env name supplying the key
// refuses to write a shadowed value.
func TestPersistMemoryRejectsEnvShadow(t *testing.T) {
	for _, name := range []string{"AGENTS_SANDBOX_MEMORY", "OPENCODE_SANDBOX_MEMORY"} {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			t.Setenv(name, "8G")

			dir, err := PersistMemory(configpaths.Get(), testSlug, 6144, false)
			if err == nil {
				t.Fatal("expected an error when the env supplies memory")
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("err = %v; want it to name %s", err, name)
			}
			if dir != "" {
				t.Errorf("dir = %q; want empty on refusal", dir)
			}
			assertNoConfigWritten(t, configpaths.Get())
		})
	}
}

// TestPersistMemoryRejectsFlagShadow pins that a changed --memory flag refuses
// to write a shadowed value.
func TestPersistMemoryRejectsFlagShadow(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	dir, err := PersistMemory(configpaths.Get(), testSlug, 6144, true)
	if err == nil {
		t.Fatal("expected an error when the --memory flag supplies memory")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("err = %v; want it to name the flag", err)
	}
	if dir != "" {
		t.Errorf("dir = %q; want empty on refusal", dir)
	}
	assertNoConfigWritten(t, configpaths.Get())
}

// TestPersistMemoryWritesToOwningLayer pins the success path: the value lands in
// the layer that already sets memory and a resolver re-read yields it.
func TestPersistMemoryWritesToOwningLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	unsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	cp := configpaths.Get()
	owner := cp.UserSlugConfigDir(testSlug)
	writeMemoryConfig(t, owner, "2G")

	dir, err := PersistMemory(cp, testSlug, 6144, false)
	if err != nil {
		t.Fatalf("PersistMemory: %v", err)
	}
	if dir != owner {
		t.Errorf("dir = %q; want the owning layer %q", dir, owner)
	}
	if got := resolveMemory(t, testSlug); got != "6144M" {
		t.Errorf("resolved memory = %q; want 6144M", got)
	}
}

// TestPersistMemoryOverwritesDifferentlyCasedOwnerKey pins the end-to-end
// Greptile fix: a project config spelling the key `Memory:` already owns it, so
// the raise is written there (in place, keeping the spelling) rather than to the
// lower-precedence user config where the project's `Memory:` would still win.
func TestPersistMemoryOverwritesDifferentlyCasedOwnerKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	unsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "Memory: 4G\n")

	dir, err := PersistMemory(cp, testSlug, 6144, false)
	if err != nil {
		t.Fatalf("PersistMemory: %v", err)
	}
	if dir != cp.ProjectConfigDir() {
		t.Errorf("dir = %q; want the project config dir %q", dir, cp.ProjectConfigDir())
	}
	if got := resolveMemory(t, testSlug); got != "6144M" {
		t.Errorf("resolved memory = %q; want 6144M (the project `Memory:` replaced)", got)
	}
}

// TestPersistMemoryDefaultsToUserSlugLayer pins that with no file layer owning
// memory the raise is written to the per-project user config.
func TestPersistMemoryDefaultsToUserSlugLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	unsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	cp := configpaths.Get()

	dir, err := PersistMemory(cp, testSlug, 8192, false)
	if err != nil {
		t.Fatalf("PersistMemory: %v", err)
	}
	if dir != cp.UserSlugConfigDir(testSlug) {
		t.Errorf("dir = %q; want the per-slug user config dir %q", dir, cp.UserSlugConfigDir(testSlug))
	}
	if got := resolveMemory(t, testSlug); got != "8192M" {
		t.Errorf("resolved memory = %q; want 8192M", got)
	}
}

// TestPersistMemoryWritesToProjectLayer pins that a project config already
// setting memory is the owner written to, so the raise is not shadowed.
func TestPersistMemoryWritesToProjectLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	unsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	cp := configpaths.Get()
	writeMemoryConfig(t, cp.ProjectConfigDir(), "4G")

	dir, err := PersistMemory(cp, testSlug, 6144, false)
	if err != nil {
		t.Fatalf("PersistMemory: %v", err)
	}
	if dir != cp.ProjectConfigDir() {
		t.Errorf("dir = %q; want the project config dir %q", dir, cp.ProjectConfigDir())
	}
	if got := resolveMemory(t, testSlug); got != "6144M" {
		t.Errorf("resolved memory = %q; want 6144M", got)
	}
}

// TestPersistMemorySurfacesWriteFailure pins that a writer refusal (here a
// non-YAML config file the writer cannot edit format-preservingly) is surfaced
// rather than swallowed.
func TestPersistMemorySurfacesWriteFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	unsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	cp := configpaths.Get()
	dir := cp.UserSlugConfigDir(testSlug)
	const json5 = "{\n  // comment\n  \"memory\": \"2G\",\n}\n"
	testutil.WriteFile(t, dir, "config.json5", json5)

	got, err := PersistMemory(cp, testSlug, 8192, false)
	if err == nil {
		t.Fatal("expected a write error for a non-YAML config file")
	}
	if got != "" {
		t.Errorf("dir = %q; want empty on a write failure", got)
	}
	data, readErr := os.ReadFile(filepath.Join(dir, "config.json5"))
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if string(data) != json5 {
		t.Errorf("config.json5 changed:\n got %q\nwant %q", data, json5)
	}
}

var errBoom = os.ErrClosed

func writeMemoryConfig(t *testing.T, dir, value string) {
	t.Helper()
	if err := viperconfig.WriteSetting(dir, "memory", value); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
}

func resolveMemory(t *testing.T, slug string) string {
	t.Helper()
	r, err := viperconfig.NewResolver(nil, configpaths.Get(), slug)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r.Config().Memory
}

func assertNoConfigWritten(t *testing.T, cp configpaths.ConfigPaths) {
	t.Helper()
	for _, dir := range []string{cp.UserSlugConfigDir(testSlug), cp.ProjectConfigDir()} {
		if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(err) {
			t.Errorf("config.yaml written in %s; want nothing written", dir)
		}
	}
}

// unsetEnv removes a variable for the duration of the test, restoring it after.
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	value, present := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			t.Setenv(name, value)
			return
		}
		_ = os.Unsetenv(name)
	})
}

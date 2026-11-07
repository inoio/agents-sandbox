// Package resourceproposal offers to raise a resource limit after a detected
// hit and persists an accepted raise to the config layer that owns the setting.
package resourceproposal

import (
	"fmt"
	"os"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

const (
	mibPerGiB = 1024
	// memoryKey is the launcher config key for the VM memory limit.
	memoryKey = "memory"
)

// Suggestion is the prefilled raise offered for a hit. BootMaxMiB is the
// maximum a raise may reach (for memory the VM's boot maximum, host RAM at
// creation); zero means none.
type Suggestion struct {
	CurrentMiB  uint32
	ProposedMiB uint32
	BootMaxMiB  uint32
}

// SuggestRaise returns the prefilled memory raise: a 25% increment over
// currentMiB, rounded up to the next whole GiB and at least currentMiB+1GiB. A
// non-zero bootMaxMiB clamps the result, and the result never falls below
// currentMiB.
func SuggestRaise(currentMiB, bootMaxMiB uint32) uint32 {
	return suggestIncrease(currentMiB, bootMaxMiB, currentMiB/4)
}

// SuggestDiskRaise returns the prefilled disk raise: a 50% increment, with the
// same rounding, floor and clamp as SuggestRaise.
func SuggestDiskRaise(currentMiB, maxMiB uint32) uint32 {
	return suggestIncrease(currentMiB, maxMiB, currentMiB/2)
}

// suggestIncrease adds incrementMiB to currentMiB, rounds up to a whole GiB,
// raises by at least 1 GiB, clamps to a non-zero maxMiB, and never falls below
// currentMiB.
func suggestIncrease(currentMiB, maxMiB, incrementMiB uint32) uint32 {
	proposedMiB := roundUpGiB(currentMiB + incrementMiB)
	if floorMiB := currentMiB + mibPerGiB; proposedMiB < floorMiB {
		proposedMiB = floorMiB
	}
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="clamping a proposal equal to maxMiB to maxMiB leaves it unchanged"
	if maxMiB > 0 && proposedMiB > maxMiB {
		proposedMiB = maxMiB
	}
	if proposedMiB < currentMiB {
		proposedMiB = currentMiB
	}
	return proposedMiB
}

// roundUpGiB rounds a MiB count up to the next whole GiB.
func roundUpGiB(mib uint32) uint32 {
	return (mib + mibPerGiB - 1) / mibPerGiB * mibPerGiB
}

// Ask offers to raise memory for a detected hit and returns the chosen MiB. It
// returns ok=false when the UI is non-interactive (never blocking) or the user
// declines. It never writes anything.
func Ask(ui termio.Prompter, hit pressure.Signal, s Suggestion) (uint32, bool, error) {
	return askRaise(ui, raiseOffer{
		question:   selectPrompt(hit),
		limitName:  "memory limit",
		maximumFor: "the host maximum",
		suggestion: s,
	})
}

// raiseOffer is one limit raise put to the user: the question, the limit's
// name, what its maximum is (for a rejected answer), and the suggestion.
type raiseOffer struct {
	question   string
	limitName  string
	maximumFor string
	suggestion Suggestion
}

// askRaise asks whether to raise the limit, then for the new size, re-prompting
// with the reason until the answer is valid. It returns ok=false when the UI is
// non-interactive or the user declines.
func askRaise(ui termio.Prompter, o raiseOffer) (uint32, bool, error) {
	if !ui.IsInteractive() {
		return 0, false, nil
	}
	s := o.suggestion
	decision, err := ui.Select(o.question, []termio.Choice{
		{Key: "raise", Label: "Raise", Description: "Set the " + o.limitName + " to " + formatMiB(s.ProposedMiB)},
		{Key: "keep", Label: "Keep", Description: "Leave the " + o.limitName + " unchanged"},
	}, "raise")
	if err != nil {
		return 0, false, err
	}
	if decision != "raise" {
		return 0, false, nil
	}
	inputPrompt := "New " + o.limitName + " (for example 6G or 6144M)"
	prompt := inputPrompt
	for {
		answer, err := ui.Input(prompt, formatMiB(s.ProposedMiB))
		if err != nil {
			return 0, false, err
		}
		newMiB, parsed := options.ParseMemoryOK(answer)
		problem := raiseProblem(answer, newMiB, parsed, s, o.maximumFor)
		if problem == "" {
			return newMiB, true, nil
		}
		prompt = problem + ". " + inputPrompt
	}
}

// raiseProblem checks that a parsed answer raises the current limit without
// exceeding the maximum, which maximumFor names. It returns the reason for a
// rejected answer, or "" when the answer is valid.
func raiseProblem(answer string, newMiB uint32, parsed bool, s Suggestion, maximumFor string) string {
	switch {
	case !parsed:
		return fmt.Sprintf("%q is not a size", answer)
	case newMiB <= s.CurrentMiB:
		return "The new limit must be above the current " + formatMiB(s.CurrentMiB)
	case s.BootMaxMiB > 0 && newMiB > s.BootMaxMiB:
		return "The new limit must not exceed " + maximumFor + " " + formatMiB(s.BootMaxMiB)
	}
	return ""
}

// selectPrompt frames the offer from the detected hit, naming the OOM victim
// when the evidence is the kernel log.
func selectPrompt(hit pressure.Signal) string {
	if hit.Evidence == pressure.EvidenceOOMLog && hit.Victim != "" {
		return fmt.Sprintf("The sandbox ran out of memory and killed %s. Raise the VM memory limit?", hit.Victim)
	}
	return "The sandbox is running low on memory. Raise the VM memory limit?"
}

// formatMiB renders a MiB count as GiB when it is a whole number of GiB, else as
// MiB.
func formatMiB(mib uint32) string {
	if mib%mibPerGiB == 0 {
		return fmt.Sprintf("%dG", mib/mibPerGiB)
	}
	return fmt.Sprintf("%dM", mib)
}

// PersistSetting writes value for the limit setting key to the config layer that
// owns the key, defaulting to the per-project user config, and returns that
// directory. It returns an error rather than writing a shadowed value when a
// flag (setByFlag) or an environment variable supplies the key.
func PersistSetting(paths configpaths.ConfigPaths, slug, key, value string, setByFlag bool) (string, error) {
	if source, shadowed := ShadowSource(key, setByFlag); shadowed {
		return "", fmt.Errorf("%s is set by %s; not writing a config value it would override", key, source)
	}
	dir, ok := viperconfig.LayerSettingOwner(paths, slug, key)
	if !ok {
		dir = paths.UserSlugConfigDir(slug)
	}
	// The owning layer may be the project config: the write goes there so the
	// raise is not shadowed. A project-config trust gate must also record the
	// launcher's own change there (specs/resources/resource-limit-proposal.md §4).
	if err := viperconfig.WriteSetting(dir, key, value); err != nil {
		return "", err
	}
	return dir, nil
}

// ShadowSource names the higher-precedence source that supplies the limit
// setting key (its --<key> flag or an environment variable), so a config value
// would not take effect. It reports false when config decides the value.
func ShadowSource(key string, setByFlag bool) (string, bool) {
	if setByFlag {
		return "the --" + key + " flag", true
	}
	for _, name := range viperconfig.EnvVarNames(key) {
		if os.Getenv(name) != "" {
			return "the " + name + " environment variable", true
		}
	}
	return "", false
}

// memorySetting renders a MiB count as the memory setting written to config.
func memorySetting(memoryMiB uint32) string {
	return fmt.Sprintf("%dM", memoryMiB)
}

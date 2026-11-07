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

// Suggestion is the prefilled raise offered for a hit. BootMaxMiB is the VM's
// boot maximum (host RAM at creation); zero means unknown.
type Suggestion struct {
	CurrentMiB  uint32
	ProposedMiB uint32
	BootMaxMiB  uint32
}

// SuggestRaise returns the prefilled raise: a 25% increment over currentMiB,
// rounded up to the next whole GiB and at least currentMiB+1GiB. A non-zero
// bootMaxMiB clamps the result, and the result never falls below currentMiB.
func SuggestRaise(currentMiB, bootMaxMiB uint32) uint32 {
	proposedMiB := roundUpGiB(currentMiB + currentMiB/4)
	if floorMiB := currentMiB + mibPerGiB; proposedMiB < floorMiB {
		proposedMiB = floorMiB
	}
	if bootMaxMiB > 0 && proposedMiB > bootMaxMiB {
		proposedMiB = bootMaxMiB
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
	if !ui.IsInteractive() {
		return 0, false, nil
	}
	decision, err := ui.Select(selectPrompt(hit), []termio.Choice{
		{Key: "raise", Label: "Raise", Description: "Set the memory limit to " + formatMiB(s.ProposedMiB)},
		{Key: "keep", Label: "Keep", Description: "Leave the memory limit unchanged"},
	}, "raise")
	if err != nil {
		return 0, false, err
	}
	if decision != "raise" {
		return 0, false, nil
	}
	prompt := memoryInputPrompt
	for {
		answer, err := ui.Input(prompt, formatMiB(s.ProposedMiB))
		if err != nil {
			return 0, false, err
		}
		newMiB, parsed := options.ParseMemoryOK(answer)
		problem := raiseProblem(answer, newMiB, parsed, s)
		if problem == "" {
			return newMiB, true, nil
		}
		prompt = problem + ". " + memoryInputPrompt
	}
}

const memoryInputPrompt = "New memory limit (for example 6G or 6144M)"

// raiseProblem checks that a parsed answer raises the current limit without
// exceeding the boot maximum. It returns the reason for a rejected answer, or ""
// when the answer is valid.
func raiseProblem(answer string, newMiB uint32, parsed bool, s Suggestion) string {
	switch {
	case !parsed:
		return fmt.Sprintf("%q is not a size", answer)
	case newMiB <= s.CurrentMiB:
		return "The new limit must be above the current " + formatMiB(s.CurrentMiB)
	case s.BootMaxMiB > 0 && newMiB > s.BootMaxMiB:
		return "The new limit must not exceed the host maximum " + formatMiB(s.BootMaxMiB)
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

// PersistMemory writes the accepted memory to the config layer that owns the
// memory key, defaulting to the per-project user config, and returns that
// directory. It returns an error rather than writing a shadowed value when the
// --memory flag or an environment variable supplies the key.
func PersistMemory(
	paths configpaths.ConfigPaths,
	slug string,
	memoryMiB uint32,
	memoryFlagChanged bool,
) (string, error) {
	if source, shadowed := MemoryShadowSource(memoryFlagChanged); shadowed {
		return "", fmt.Errorf("memory is set by %s; not writing a config value it would override", source)
	}
	dir, ok := viperconfig.LayerSettingOwner(paths, slug, memoryKey)
	if !ok {
		dir = paths.UserSlugConfigDir(slug)
	}
	// The owning layer may be the project config: the write goes there so the
	// raise is not shadowed. A project-config trust gate must also record the
	// launcher's own change there (specs/resources/resource-limit-proposal.md §4).
	if err := viperconfig.WriteSetting(dir, memoryKey, memorySetting(memoryMiB)); err != nil {
		return "", err
	}
	return dir, nil
}

// MemoryShadowSource names the higher-precedence source that supplies memory
// (the --memory flag or a memory environment variable), so a config value would
// not take effect. It reports false when config decides the value.
func MemoryShadowSource(memoryFlagChanged bool) (string, bool) {
	if memoryFlagChanged {
		return "the --memory flag", true
	}
	for _, name := range viperconfig.EnvVarNames(memoryKey) {
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

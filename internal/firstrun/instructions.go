package firstrun

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// The seed copies at most this many instruction files and bytes, so a large
// skills tree is not snapshotted into the managed scope and mirrored on every
// run. Each file is also bounded by readback.MaxHashedBytes.
const (
	maxSeedInstructionFiles = 200
	maxSeedInstructionBytes = 2 << 20
)

// instructionFileExtension is the only file type the seed copies from an
// instruction tree: free text the secret scan can judge, never a script.
const instructionFileExtension = ".md"

// prunedInstructionDirs are directories never descended into inside an
// instruction tree: a checkout's history and installed dependencies.
var prunedInstructionDirs = []string{".git", "node_modules"}

// HostInstructionFiles returns the host paths of a's instruction files the seed
// would copy, sorted (see planInstructions). They count for the first-run offer
// but stay out of the source hash, so editing one does not re-arm the decision.
func HostInstructionFiles(a agent.Agent, hostHome string) []string {
	var files []string
	for _, unit := range planInstructions(a, hostHome).units {
		for _, source := range unit.sources {
			files = append(files, source.hostPath)
		}
	}
	sort.Strings(files)
	return files
}

// instructionAllowlist returns the root a's instruction paths are relative to
// (the directory of its VM config file under home) and the paths themselves; an
// empty root and no paths when a declares none.
func instructionAllowlist(a agent.Agent, home string) (string, []string) {
	seeder, ok := agent.AsInstructionSeeder(a)
	if !ok {
		return "", nil
	}
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		return "", nil
	}
	return filepath.Dir(merger.VMConfigPath(home)), seeder.InstructionPaths()
}

// isInstructionFileName reports whether name is a Markdown file, in any case.
func isInstructionFileName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), instructionFileExtension)
}

// instructionFileNames returns a's top-level instruction file names (the
// allowlist entries that are files, not trees). A managed config dir holding
// only these is not a managed config layer.
func instructionFileNames(a agent.Agent) []string {
	_, entries := instructionAllowlist(a, "")
	var names []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry, "/") {
			names = append(names, entry)
		}
	}
	return names
}

// instructionSeedOutcome is what the seed copied and what it left uncopied, by
// reason.
type instructionSeedOutcome struct {
	copied         int
	unseedable     []string // unreadable, over the per-file cap, or failed to write
	secretShaped   []string // a line looks like a literal credential
	scriptedSkills []string // root-relative skill dirs holding a non-Markdown file
	escapingLinks  []string // symlinks resolving outside the home, to it, or nowhere
	overCap        int      // files of the units past the seed's file-count or byte cap
}

// seedInstructions copies a's planned instruction units (planInstructions) to
// the same root-relative paths under targetDir, the managed config dir the
// mirror copies into the VM's config dir. Each unit is admitted or refused
// whole, in sorted order: it is refused when a file is unreadable or any line
// looks like a literal credential (readback.TextLeaksSecret), and skipped when
// it would take the copy past maxSeedInstructionFiles or
// maxSeedInstructionBytes, while a later smaller unit may still fit.
func seedInstructions(a agent.Agent, hostHome, targetDir string) instructionSeedOutcome {
	plan := planInstructions(a, hostHome)
	outcome := instructionSeedOutcome{
		copied:         0,
		unseedable:     nil,
		secretShaped:   nil,
		scriptedSkills: plan.scriptedSkills,
		escapingLinks:  plan.escapingLinks,
		overCap:        0,
	}
	copiedBytes := 0
	for _, unit := range plan.units {
		contents, unreadable, secretShaped := readInstructionUnit(unit)
		outcome.unseedable = append(outcome.unseedable, unreadable...)
		outcome.secretShaped = append(outcome.secretShaped, secretShaped...)
		if len(unreadable) > 0 || len(secretShaped) > 0 {
			continue
		}
		unitBytes := 0
		for _, content := range contents {
			unitBytes += len(content)
		}
		if outcome.copied+len(contents) > maxSeedInstructionFiles ||
			copiedBytes+unitBytes > maxSeedInstructionBytes {
			outcome.overCap += len(contents)
			continue
		}
		for index, source := range unit.sources {
			err := atomicwrite.WriteFileMode(filepath.Join(targetDir, source.rel), contents[index], managedConfigMode)
			if err != nil {
				outcome.unseedable = append(outcome.unseedable, source.hostPath)
				continue
			}
			outcome.copied++
			copiedBytes += len(contents[index])
		}
	}
	return outcome
}

// readInstructionUnit reads every file of unit from its resolved path under the
// read-back cap, and returns their contents with the host paths it could not
// read and those whose content looks like it holds a secret.
func readInstructionUnit(unit instructionUnit) ([][]byte, []string, []string) {
	var contents [][]byte
	var unreadable, secretShaped []string
	for _, source := range unit.sources {
		content, ok := readback.ReadCappedFile(source.readPath, readback.MaxHashedBytes)
		switch {
		case !ok:
			unreadable = append(unreadable, source.hostPath)
		case readback.TextLeaksSecret(content):
			secretShaped = append(secretShaped, source.hostPath)
		}
		contents = append(contents, content)
	}
	return contents, unreadable, secretShaped
}
